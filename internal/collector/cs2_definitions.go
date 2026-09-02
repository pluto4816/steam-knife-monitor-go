package collector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	cs2DefinitionsUserAgent        = "steam-knife-monitor-go/2 candidate-updater"
	maxCS2DefinitionsResponseBytes = 64 << 20
	requiredRarityID               = "rarity_rare_weapon"
	requiredRarityName             = "Mil-Spec Grade"
	requiredWearID                 = "SFUI_InvTooltip_Wear_Amount_2"
	requiredWearName               = "Field-Tested"
)

var ErrCS2DefinitionsAccessRestricted = errors.New("CS2 definitions access restricted")

type CS2DefinitionsClient struct {
	endpoint   string
	httpClient *http.Client
}

type CS2DefinitionCandidate struct {
	MarketHashName string
}

type CS2DefinitionSnapshot struct {
	Candidates  []CS2DefinitionCandidate
	RecordCount int
	SHA256      string
}

func NewCS2DefinitionsClient(endpoint string, httpClient *http.Client) *CS2DefinitionsClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Minute}
	}
	return &CS2DefinitionsClient{
		endpoint:   strings.TrimSpace(endpoint),
		httpClient: httpClient,
	}
}

func (client *CS2DefinitionsClient) FetchCandidateSnapshot(ctx context.Context) (CS2DefinitionSnapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint, nil)
	if err != nil {
		return CS2DefinitionSnapshot{}, fmt.Errorf("create CS2 definitions request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", cs2DefinitionsUserAgent)

	response, err := client.httpClient.Do(request)
	if err != nil {
		return CS2DefinitionSnapshot{}, fmt.Errorf("request CS2 definitions: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusUnauthorized ||
		response.StatusCode == http.StatusForbidden ||
		response.StatusCode == http.StatusTooManyRequests {
		detail := fmt.Sprintf("HTTP %d", response.StatusCode)
		if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
			detail += "; Retry-After=" + retryAfter
		}
		return CS2DefinitionSnapshot{}, fmt.Errorf("%w: %s", ErrCS2DefinitionsAccessRestricted, detail)
	}
	if response.ContentLength > maxCS2DefinitionsResponseBytes {
		return CS2DefinitionSnapshot{}, fmt.Errorf(
			"CS2 definitions response is too large: Content-Length %d exceeds %d bytes",
			response.ContentLength,
			maxCS2DefinitionsResponseBytes,
		)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxCS2DefinitionsResponseBytes+1))
	if err != nil {
		return CS2DefinitionSnapshot{}, fmt.Errorf("read CS2 definitions response: %w", err)
	}
	if len(responseBody) > maxCS2DefinitionsResponseBytes {
		return CS2DefinitionSnapshot{}, fmt.Errorf(
			"CS2 definitions response exceeds %d bytes",
			maxCS2DefinitionsResponseBytes,
		)
	}
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return CS2DefinitionSnapshot{}, fmt.Errorf(
			"%w: HTTP %d with empty response",
			ErrCS2DefinitionsAccessRestricted,
			response.StatusCode,
		)
	}
	if looksLikeChallenge(response.Header.Get("Content-Type"), responseBody) {
		return CS2DefinitionSnapshot{}, fmt.Errorf(
			"%w: challenge-like HTML",
			ErrCS2DefinitionsAccessRestricted,
		)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return CS2DefinitionSnapshot{}, fmt.Errorf("HTTP %d from CS2 definitions source", response.StatusCode)
	}

	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	var records []cs2DefinitionRecord
	if err := decoder.Decode(&records); err != nil {
		return CS2DefinitionSnapshot{}, fmt.Errorf("decode CS2 definitions response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return CS2DefinitionSnapshot{}, fmt.Errorf("decode CS2 definitions response: multiple JSON values")
		}
		return CS2DefinitionSnapshot{}, fmt.Errorf("decode CS2 definitions response: %w", err)
	}
	if len(records) == 0 {
		return CS2DefinitionSnapshot{}, fmt.Errorf("CS2 definitions response contains no records")
	}

	candidates := make([]CS2DefinitionCandidate, 0)
	seenNames := make(map[string]struct{})
	for index, record := range records {
		if strings.TrimSpace(record.MarketHashName) == "" {
			return CS2DefinitionSnapshot{}, fmt.Errorf(
				"CS2 definition record %d has empty market_hash_name",
				index,
			)
		}
		if record.Rarity == nil || record.Rarity.ID != requiredRarityID {
			continue
		}
		if record.Rarity.Name != requiredRarityName {
			return CS2DefinitionSnapshot{}, fmt.Errorf(
				"CS2 definition %q has rarity name %q for ID %q",
				record.MarketHashName,
				record.Rarity.Name,
				record.Rarity.ID,
			)
		}
		if record.Wear == nil {
			return CS2DefinitionSnapshot{}, fmt.Errorf(
				"CS2 definition %q has no wear for rarity %q",
				record.MarketHashName,
				requiredRarityID,
			)
		}
		if record.Wear.Name != requiredWearName {
			continue
		}
		if record.Wear.ID != requiredWearID {
			return CS2DefinitionSnapshot{}, fmt.Errorf(
				"CS2 definition %q has wear ID %q for %q",
				record.MarketHashName,
				record.Wear.ID,
				requiredWearName,
			)
		}
		if record.StatTrak == nil || record.Souvenir == nil {
			return CS2DefinitionSnapshot{}, fmt.Errorf(
				"CS2 definition %q is missing quality flags",
				record.MarketHashName,
			)
		}
		if *record.StatTrak || *record.Souvenir {
			continue
		}
		if !strings.HasSuffix(record.MarketHashName, " (Field-Tested)") {
			return CS2DefinitionSnapshot{}, fmt.Errorf(
				"CS2 definition %q conflicts with Field-Tested wear",
				record.MarketHashName,
			)
		}
		if strings.HasPrefix(record.MarketHashName, "StatTrak™ ") ||
			strings.HasPrefix(record.MarketHashName, "Souvenir ") {
			return CS2DefinitionSnapshot{}, fmt.Errorf(
				"CS2 definition %q conflicts with normal quality flags",
				record.MarketHashName,
			)
		}
		if _, exists := seenNames[record.MarketHashName]; exists {
			return CS2DefinitionSnapshot{}, fmt.Errorf(
				"duplicate CS2 definition market_hash_name %q",
				record.MarketHashName,
			)
		}
		seenNames[record.MarketHashName] = struct{}{}
		candidates = append(candidates, CS2DefinitionCandidate{MarketHashName: record.MarketHashName})
	}
	if len(candidates) == 0 {
		return CS2DefinitionSnapshot{}, fmt.Errorf("CS2 definitions filter returned no candidates")
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].MarketHashName < candidates[right].MarketHashName
	})

	digest := sha256.Sum256(responseBody)
	return CS2DefinitionSnapshot{
		Candidates:  candidates,
		RecordCount: len(records),
		SHA256:      hex.EncodeToString(digest[:]),
	}, nil
}

type cs2DefinitionRecord struct {
	MarketHashName string                 `json:"market_hash_name"`
	Rarity         *cs2DefinitionCategory `json:"rarity"`
	Wear           *cs2DefinitionCategory `json:"wear"`
	StatTrak       *bool                  `json:"stattrak"`
	Souvenir       *bool                  `json:"souvenir"`
}

type cs2DefinitionCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
