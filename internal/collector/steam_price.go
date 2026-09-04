package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
)

const (
	steamPricePath         = "/market/priceoverview/"
	steamPriceAppID        = "730"
	steamPriceCNYCurrency  = "23"
	steamPriceMaxBodyBytes = 64 << 10
	steamPriceUserAgent    = "steam-knife-monitor-go/2 price-verifier"
)

var ErrSteamAccessRestricted = errors.New("Steam Community Market access restricted")

type SteamPriceClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewSteamPriceClient(baseURL string, httpClient *http.Client) *SteamPriceClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &SteamPriceClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

func (client *SteamPriceClient) FetchLowestPrice(
	ctx context.Context,
	marketHashName string,
) (string, error) {
	if strings.TrimSpace(marketHashName) == "" {
		return "", fmt.Errorf("Steam market_hash_name cannot be empty")
	}
	endpoint, err := url.Parse(client.baseURL + steamPricePath)
	if err != nil {
		return "", fmt.Errorf("create Steam price URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("appid", steamPriceAppID)
	query.Set("currency", steamPriceCNYCurrency)
	query.Set("market_hash_name", marketHashName)
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("create Steam price request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", steamPriceUserAgent)

	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("request Steam price: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, steamPriceMaxBodyBytes+1))
	if err != nil {
		return "", fmt.Errorf("read Steam price response: %w", err)
	}
	if len(responseBody) > steamPriceMaxBodyBytes {
		return "", fmt.Errorf("Steam price response exceeds %d bytes", steamPriceMaxBodyBytes)
	}
	if response.StatusCode == http.StatusUnauthorized ||
		response.StatusCode == http.StatusForbidden ||
		response.StatusCode == http.StatusTooManyRequests {
		detail := fmt.Sprintf("HTTP %d", response.StatusCode)
		if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
			detail += "; Retry-After=" + retryAfter
		}
		return "", fmt.Errorf("%w: %s", ErrSteamAccessRestricted, detail)
	}
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return "", fmt.Errorf(
			"%w: HTTP %d with empty response",
			ErrSteamAccessRestricted,
			response.StatusCode,
		)
	}
	if looksLikeChallenge(response.Header.Get("Content-Type"), responseBody) {
		return "", fmt.Errorf("%w: challenge-like HTML", ErrSteamAccessRestricted)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d from Steam priceoverview", response.StatusCode)
	}

	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	var raw struct {
		Success     bool   `json:"success"`
		LowestPrice string `json:"lowest_price"`
	}
	if err := decoder.Decode(&raw); err != nil {
		return "", fmt.Errorf("decode Steam price response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", fmt.Errorf("decode Steam price response: multiple JSON values")
		}
		return "", fmt.Errorf("decode Steam price response: %w", err)
	}
	if !raw.Success {
		return "", fmt.Errorf("Steam price response reported failure")
	}
	price, err := parseSteamCNYPrice(raw.LowestPrice)
	if err != nil {
		return "", fmt.Errorf("parse Steam lowest_price: %w", err)
	}
	return price, nil
}

func parseSteamCNYPrice(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(value, "¥"):
		value = strings.TrimSpace(strings.TrimPrefix(value, "¥"))
	case strings.HasPrefix(value, "￥"):
		value = strings.TrimSpace(strings.TrimPrefix(value, "￥"))
	default:
		return "", fmt.Errorf("price is not CNY")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return "", fmt.Errorf("CNY price %q is not a plain decimal", value)
	}
	groups := strings.Split(parts[0], ",")
	for index, group := range groups {
		if group == "" || (len(groups) > 1 && index == 0 && len(group) > 3) ||
			(index > 0 && len(group) != 3) {
			return "", fmt.Errorf("CNY price %q has invalid grouping", value)
		}
		for _, character := range group {
			if character < '0' || character > '9' {
				return "", fmt.Errorf("CNY price %q is not a plain decimal", value)
			}
		}
	}
	whole := strings.Join(groups, "")
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) == 0 || len(fraction) > 2 {
			return "", fmt.Errorf("CNY price %q has invalid fen precision", value)
		}
		for _, character := range fraction {
			if character < '0' || character > '9' {
				return "", fmt.Errorf("CNY price %q is not a plain decimal", value)
			}
		}
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	amount := new(big.Int)
	if _, ok := amount.SetString(whole+fraction, 10); !ok || amount.Sign() <= 0 {
		return "", fmt.Errorf("CNY price must be positive")
	}
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	return whole + "." + fraction, nil
}
