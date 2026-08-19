package collector

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

const (
	searchPath   = "/api/commodity/search"
	exchangePath = "/api/commodity/exchange"
	pageSize     = 50

	// These values are current smis.club frontend behavior verified in the
	// frozen Python investigation. They may need updating when the site changes.
	authKey = "CMDDTYDF&WY196KJ"
	authIV  = "CMDDTYDF&WY196KJ"
	auth2   = "3d4b7647-283e-5f5b-8c1c-65ff8b166e97"
)

var ErrAccessRestricted = errors.New("smis access restricted")

type SMISClient struct {
	baseURL    string
	httpClient *http.Client
}

type Candidate struct {
	ItemID int64
	NameCN string
}

type MarketItem struct {
	ItemID         int64
	MarketHashName string
	NameCN         string
	MarketData     core.MarketData
	SteamPrice     string
}

func NewSMISClient(baseURL string, httpClient *http.Client) *SMISClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &SMISClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

func (client *SMISClient) FetchCandidatePage(ctx context.Context, page int) ([]Candidate, error) {
	if page < 1 {
		return nil, fmt.Errorf("candidate page must be at least 1")
	}
	payload := candidateRequest{
		PageNum:      page,
		PageSize:     pageSize,
		Game:         "csgo",
		Name:         "",
		TypeList:     []string{},
		RarityList:   []string{"军规级"},
		QualityList:  []string{"普通"},
		ExteriorList: []string{"久经沙场"},
		WeaponList:   []string{},
		HeroList:     []string{},
		SlotList:     []string{},
	}
	data, err := client.post(ctx, searchPath, payload)
	if err != nil {
		return nil, err
	}
	object, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("search response data must be an object")
	}
	rawItems, ok := object["items"].([]any)
	if !ok {
		return nil, fmt.Errorf("search response data.items must be an array")
	}
	items := make([]Candidate, 0, len(rawItems))
	for _, rawItem := range rawItems {
		record, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		itemID, ok := positiveID(record["id"])
		if !ok {
			continue
		}
		items = append(items, Candidate{
			ItemID: itemID,
			NameCN: stringValue(record["cnName"]),
		})
	}
	return items, nil
}

func (client *SMISClient) FetchExchangePage(ctx context.Context, page int) ([]MarketItem, error) {
	if page < 1 {
		return nil, fmt.Errorf("exchange page must be at least 1")
	}
	payload := exchangeRequest{
		Model:                   0,
		Platforms:               []string{"buff", "uuyp", "c5", "igxe", "eco"},
		AppID:                   "730",
		MinPrice:                json.Number("30"),
		MaxPrice:                json.Number("5000"),
		MinTransactionQuantity:  50,
		MinWeekGrowthRate:       json.Number("-0.1"),
		MaxWeekGrowthRate:       json.Number("0.1"),
		MinRate:                 json.Number("0"),
		MaxRate:                 json.Number("1.5"),
		MinSteamSellSubBuyRatio: json.Number("0.3"),
		TimeAfter:               nil,
		PageNum:                 page,
		PageSize:                pageSize,
		BuffTax:                 json.Number("0.015"),
		BuffLimit:               9999999,
		UUYPTax:                 json.Number("0.01"),
		UUYPLimit:               9999999,
		C5Tax:                   json.Number("0.01"),
		C5Limit:                 100,
		IGXETax:                 json.Number("0.006"),
		IGXELimit:               9999999,
		EcoTax:                  json.Number("0"),
		EcoLimit:                9999999,
	}
	data, err := client.post(ctx, exchangePath, payload)
	if err != nil {
		return nil, err
	}
	rawItems, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("exchange response data must be an array")
	}
	items := make([]MarketItem, 0, len(rawItems))
	for _, rawItem := range rawItems {
		record, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		item, ok := parseMarketItem(record)
		if ok {
			items = append(items, item)
		}
	}
	return items, nil
}

func (client *SMISClient) post(ctx context.Context, path string, payload any) (any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode request for %s: %w", path, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", path, err)
	}
	auth, err := makeAuth(strconv.FormatInt(time.Now().UnixMilli(), 10))
	if err != nil {
		return nil, fmt.Errorf("create Auth header: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Auth", auth)
	request.Header.Set("Auth2", auth2)

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", path, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", path, err)
	}

	if response.StatusCode == http.StatusUnauthorized ||
		response.StatusCode == http.StatusForbidden ||
		response.StatusCode == http.StatusTooManyRequests {
		detail := fmt.Sprintf("HTTP %d from %s", response.StatusCode, path)
		if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
			detail += "; Retry-After=" + retryAfter
		}
		return nil, fmt.Errorf("%w: %s", ErrAccessRestricted, detail)
	}
	if looksLikeChallenge(response.Header.Get("Content-Type"), responseBody) {
		return nil, fmt.Errorf("%w: challenge-like HTML from %s", ErrAccessRestricted, path)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d from %s", response.StatusCode, path)
	}

	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	var envelope map[string]any
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode JSON response from %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode JSON response from %s: multiple values", path)
		}
		return nil, fmt.Errorf("decode JSON response from %s: %w", path, err)
	}
	code, ok := envelope["code"].(json.Number)
	if !ok || code.String() != "200" {
		return nil, fmt.Errorf("unexpected smis envelope code %v from %s", envelope["code"], path)
	}
	data, ok := envelope["data"]
	if !ok {
		return nil, fmt.Errorf("smis envelope from %s is missing data", path)
	}
	return data, nil
}

func makeAuth(timestamp string) (string, error) {
	block, err := aes.NewCipher([]byte(authKey))
	if err != nil {
		return "", err
	}
	paddingLength := aes.BlockSize - len(timestamp)%aes.BlockSize
	padded := make([]byte, len(timestamp)+paddingLength)
	copy(padded, timestamp)
	for index := len(timestamp); index < len(padded); index++ {
		padded[index] = byte(paddingLength)
	}
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte(authIV)).CryptBlocks(encrypted, padded)
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func parseMarketItem(record map[string]any) (MarketItem, bool) {
	itemID, ok := positiveID(record["id"])
	if !ok {
		return MarketItem{}, false
	}
	ratioNumber, ok := record["extremeRatio"].(json.Number)
	if !ok {
		return MarketItem{}, false
	}
	ratio, err := core.ParseRatio(ratioNumber.String())
	if err != nil {
		return MarketItem{}, false
	}
	volumeNumber, ok := record["steamTransactionQuantity"].(json.Number)
	if !ok {
		return MarketItem{}, false
	}
	volume, err := volumeNumber.Int64()
	if err != nil || volume < 0 || (strconv.IntSize == 32 && volume > 1<<31-1) {
		return MarketItem{}, false
	}
	steamPrice := ""
	if price, ok := record["steamSellPrice"].(json.Number); ok {
		steamPrice = price.String()
	}
	return MarketItem{
		ItemID:         itemID,
		MarketHashName: stringValue(record["hashName"]),
		NameCN:         stringValue(record["cnName"]),
		MarketData: core.MarketData{
			Ratio:            ratio,
			SteamDailyVolume: int(volume),
		},
		SteamPrice: steamPrice,
	}, true
}

func positiveID(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	id, err := number.Int64()
	return id, err == nil && id > 0
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func looksLikeChallenge(contentType string, body []byte) bool {
	trimmed := strings.TrimSpace(strings.ToLower(string(body)))
	isHTML := strings.Contains(strings.ToLower(contentType), "text/html") ||
		strings.HasPrefix(trimmed, "<!doctype html") ||
		strings.HasPrefix(trimmed, "<html")
	if !isHTML {
		return false
	}
	for _, marker := range []string{"cloudflare", "captcha", "challenge", "aliyuncaptcha", "cf-chl"} {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

type candidateRequest struct {
	PageNum      int      `json:"pageNum"`
	PageSize     int      `json:"pageSize"`
	Game         string   `json:"game"`
	Name         string   `json:"name"`
	TypeList     []string `json:"typeList"`
	RarityList   []string `json:"rarityList"`
	QualityList  []string `json:"qualityList"`
	ExteriorList []string `json:"exteriorList"`
	WeaponList   []string `json:"weaponList"`
	HeroList     []string `json:"heroList"`
	SlotList     []string `json:"slotList"`
}

type exchangeRequest struct {
	Model                   int         `json:"model"`
	Platforms               []string    `json:"platforms"`
	AppID                   string      `json:"appid"`
	MinPrice                json.Number `json:"minPrice"`
	MaxPrice                json.Number `json:"maxPrice"`
	MinTransactionQuantity  int         `json:"minTransactionQuantity"`
	MinWeekGrowthRate       json.Number `json:"minWeekGrowthRate"`
	MaxWeekGrowthRate       json.Number `json:"maxWeekGrowthRate"`
	MinRate                 json.Number `json:"minRate"`
	MaxRate                 json.Number `json:"maxRate"`
	MinSteamSellSubBuyRatio json.Number `json:"minSteamSellSubBuyRatio"`
	TimeAfter               *string     `json:"timeAfter"`
	PageNum                 int         `json:"pageNum"`
	PageSize                int         `json:"pageSize"`
	BuffTax                 json.Number `json:"buffTax"`
	BuffLimit               int         `json:"buffLimit"`
	UUYPTax                 json.Number `json:"uuypTax"`
	UUYPLimit               int         `json:"uuypLimit"`
	C5Tax                   json.Number `json:"c5Tax"`
	C5Limit                 int         `json:"c5Limit"`
	IGXETax                 json.Number `json:"igxeTax"`
	IGXELimit               int         `json:"igxeLimit"`
	EcoTax                  json.Number `json:"ecoTax"`
	EcoLimit                int         `json:"ecoLimit"`
}
