package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	steamSearchPath = "/market/search/render/"
	steamAppID      = 730
	steamUserAgent  = "steam-knife-monitor-go/2 candidate-updater"
)

var ErrSteamAccessRestricted = errors.New("Steam Community Market access restricted")

type SteamClient struct {
	baseURL    string
	httpClient *http.Client
}

type SteamCandidate struct {
	MarketHashName string
}

type SteamCandidatePage struct {
	Items      []SteamCandidate
	Start      int
	PageSize   int
	TotalCount int
}

func NewSteamClient(baseURL string, httpClient *http.Client) *SteamClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &SteamClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

func (client *SteamClient) FetchCandidatePage(ctx context.Context, start, count int) (SteamCandidatePage, error) {
	if start < 0 {
		return SteamCandidatePage{}, fmt.Errorf("Steam candidate start cannot be negative")
	}
	if count <= 0 || count > 100 {
		return SteamCandidatePage{}, fmt.Errorf("Steam candidate count must be between 1 and 100")
	}

	endpoint, err := url.Parse(client.baseURL + steamSearchPath)
	if err != nil {
		return SteamCandidatePage{}, fmt.Errorf("create Steam candidate URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("query", "")
	query.Set("start", strconv.Itoa(start))
	query.Set("count", strconv.Itoa(count))
	query.Set("search_descriptions", "0")
	query.Set("sort_column", "name")
	query.Set("sort_dir", "asc")
	query.Set("appid", strconv.Itoa(steamAppID))
	query.Add("category_730_Quality[]", "tag_normal")
	query.Add("category_730_Exterior[]", "tag_WearCategory2")
	query.Add("category_730_Rarity[]", "tag_Rarity_Rare_Weapon")
	query.Set("norender", "1")
	query.Set("l", "english")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return SteamCandidatePage{}, fmt.Errorf("create Steam candidate request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", steamUserAgent)

	response, err := client.httpClient.Do(request)
	if err != nil {
		return SteamCandidatePage{}, fmt.Errorf("request Steam candidates: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return SteamCandidatePage{}, fmt.Errorf("read Steam candidate response: %w", err)
	}

	if response.StatusCode == http.StatusUnauthorized ||
		response.StatusCode == http.StatusForbidden ||
		response.StatusCode == http.StatusTooManyRequests {
		detail := fmt.Sprintf("HTTP %d", response.StatusCode)
		if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
			detail += "; Retry-After=" + retryAfter
		}
		return SteamCandidatePage{}, fmt.Errorf("%w: %s", ErrSteamAccessRestricted, detail)
	}
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return SteamCandidatePage{}, fmt.Errorf("%w: HTTP %d with empty response", ErrSteamAccessRestricted, response.StatusCode)
	}
	if looksLikeChallenge(response.Header.Get("Content-Type"), responseBody) {
		return SteamCandidatePage{}, fmt.Errorf("%w: challenge-like HTML", ErrSteamAccessRestricted)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SteamCandidatePage{}, fmt.Errorf("HTTP %d from Steam candidate search", response.StatusCode)
	}

	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	var raw steamSearchResponse
	if err := decoder.Decode(&raw); err != nil {
		return SteamCandidatePage{}, fmt.Errorf("decode Steam candidate response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return SteamCandidatePage{}, fmt.Errorf("decode Steam candidate response: multiple JSON values")
		}
		return SteamCandidatePage{}, fmt.Errorf("decode Steam candidate response: %w", err)
	}
	if !raw.Success {
		return SteamCandidatePage{}, fmt.Errorf("Steam candidate response reported failure")
	}
	if raw.Start != start {
		return SteamCandidatePage{}, fmt.Errorf("Steam candidate response start %d does not match request %d", raw.Start, start)
	}
	if raw.PageSize <= 0 || raw.PageSize > count {
		return SteamCandidatePage{}, fmt.Errorf("invalid Steam candidate pagesize %d", raw.PageSize)
	}
	if raw.TotalCount < 0 || len(raw.Results) > raw.PageSize {
		return SteamCandidatePage{}, fmt.Errorf("invalid Steam candidate result counts")
	}

	items := make([]SteamCandidate, 0, len(raw.Results))
	for index, result := range raw.Results {
		hashName := result.AssetDescription.MarketHashName
		if hashName == "" {
			hashName = result.HashName
		}
		if hashName == "" {
			return SteamCandidatePage{}, fmt.Errorf("Steam candidate result %d has empty market_hash_name", index)
		}
		if result.HashName != "" && result.HashName != hashName {
			return SteamCandidatePage{}, fmt.Errorf("Steam candidate result %d has conflicting hash names", index)
		}
		if result.AssetDescription.AppID != steamAppID {
			return SteamCandidatePage{}, fmt.Errorf("Steam candidate %q has appid %d", hashName, result.AssetDescription.AppID)
		}
		if !strings.HasPrefix(result.AssetDescription.Type, "Mil-Spec Grade ") {
			return SteamCandidatePage{}, fmt.Errorf("Steam candidate %q has type %q", hashName, result.AssetDescription.Type)
		}
		if !strings.HasSuffix(hashName, " (Field-Tested)") {
			return SteamCandidatePage{}, fmt.Errorf("Steam candidate %q is not Field-Tested", hashName)
		}
		if strings.HasPrefix(hashName, "StatTrak™ ") || strings.HasPrefix(hashName, "Souvenir ") {
			return SteamCandidatePage{}, fmt.Errorf("Steam candidate %q is not Normal quality", hashName)
		}
		items = append(items, SteamCandidate{MarketHashName: hashName})
	}

	return SteamCandidatePage{
		Items:      items,
		Start:      raw.Start,
		PageSize:   raw.PageSize,
		TotalCount: raw.TotalCount,
	}, nil
}

type steamSearchResponse struct {
	Success    bool                `json:"success"`
	Start      int                 `json:"start"`
	PageSize   int                 `json:"pagesize"`
	TotalCount int                 `json:"total_count"`
	Results    []steamSearchResult `json:"results"`
}

type steamSearchResult struct {
	HashName         string `json:"hash_name"`
	AssetDescription struct {
		AppID          int    `json:"appid"`
		Type           string `json:"type"`
		MarketHashName string `json:"market_hash_name"`
	} `json:"asset_description"`
}
