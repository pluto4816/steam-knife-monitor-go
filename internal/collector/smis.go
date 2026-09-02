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
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

const (
	searchPath   = "/api/commodity/search"
	exchangePath = "/api/commodity/exchange"
	detailPath   = "/api/commodity/%d"
	pageSize     = 50
	SMISPageSize = pageSize
	maxRatioBP   = int64(1<<31 - 1)

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

type CandidatePage struct {
	Items    []Candidate
	RawCount int
	PageNum  int64
	PageSize int64
}

type MarketItem struct {
	ItemID              int64
	MarketHashName      string
	NameCN              string
	MarketData          core.MarketData
	SteamPrice          string
	LowestPlatform      string
	LowestPlatformPrice string
	UpdatedAt           string
}

type ExchangePage struct {
	Items    []MarketItem
	RawCount int
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

func (client *SMISClient) FetchCandidatePage(ctx context.Context, page int) (CandidatePage, error) {
	if page < 1 {
		return CandidatePage{}, fmt.Errorf("candidate page must be at least 1")
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
		return CandidatePage{}, err
	}
	object, ok := data.(map[string]any)
	if !ok {
		return CandidatePage{}, fmt.Errorf("search response data must be an object")
	}
	rawItems, ok := object["items"].([]any)
	if !ok {
		return CandidatePage{}, fmt.Errorf("search response data.items must be an array")
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
	responsePageNum, _ := positiveID(object["pageNum"])
	responsePageSize, _ := positiveID(object["pageSize"])
	return CandidatePage{
		Items:    items,
		RawCount: len(rawItems),
		PageNum:  responsePageNum,
		PageSize: responsePageSize,
	}, nil
}

func (client *SMISClient) FetchExchangePage(ctx context.Context, page int) (ExchangePage, error) {
	if page < 1 {
		return ExchangePage{}, fmt.Errorf("exchange page must be at least 1")
	}
	payload := exchangeRequest{
		Model:                   0,
		Platforms:               []string{"buff", "uuyp", "c5", "igxe", "eco"},
		AppID:                   "730",
		MinPrice:                nil,
		MaxPrice:                nil,
		MinTransactionQuantity:  nil,
		MinWeekGrowthRate:       nil,
		MaxWeekGrowthRate:       nil,
		MinRate:                 nil,
		MaxRate:                 nil,
		MinSteamSellSubBuyRatio: nil,
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
		return ExchangePage{}, err
	}
	rawItems, ok := data.([]any)
	if !ok {
		return ExchangePage{}, fmt.Errorf("exchange response data must be an array")
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
	return ExchangePage{Items: items, RawCount: len(rawItems)}, nil
}

func (client *SMISClient) FetchMarketItem(ctx context.Context, itemID int64) (MarketItem, error) {
	if itemID <= 0 {
		return MarketItem{}, fmt.Errorf("item ID must be positive")
	}
	path := fmt.Sprintf(detailPath, itemID)
	data, err := client.get(ctx, path)
	if err != nil {
		return MarketItem{}, err
	}
	record, ok := data.(map[string]any)
	if !ok {
		return MarketItem{}, fmt.Errorf("detail response data must be an object")
	}
	item, err := parseDetailMarketItem(record)
	if err != nil {
		return MarketItem{}, fmt.Errorf("parse detail item %d: %w", itemID, err)
	}
	if item.ItemID != itemID {
		return MarketItem{}, fmt.Errorf("detail response ID %d does not match requested ID %d", item.ItemID, itemID)
	}
	return item, nil
}

func (client *SMISClient) get(ctx context.Context, path string) (any, error) {
	return client.request(ctx, http.MethodGet, path, nil)
}

func (client *SMISClient) post(ctx context.Context, path string, payload any) (any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode request for %s: %w", path, err)
	}
	return client.request(ctx, http.MethodPost, path, bytes.NewReader(body))
}

func (client *SMISClient) request(ctx context.Context, method, path string, body io.Reader) (any, error) {
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", path, err)
	}
	auth, err := makeAuth(strconv.FormatInt(time.Now().UnixMilli(), 10))
	if err != nil {
		return nil, fmt.Errorf("create Auth header: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
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
	if response.StatusCode >= 200 && response.StatusCode < 300 && len(bytes.TrimSpace(responseBody)) == 0 {
		return nil, fmt.Errorf("%w: HTTP %d with empty response from %s", ErrAccessRestricted, response.StatusCode, path)
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
		UpdatedAt:  stringValue(record["updateTime"]),
	}, true
}

func parseDetailMarketItem(record map[string]any) (MarketItem, error) {
	itemID, ok := positiveID(record["id"])
	if !ok {
		return MarketItem{}, fmt.Errorf("invalid id")
	}

	volumeNumber, ok := record["steamTransactionQuantity"].(json.Number)
	if !ok {
		return MarketItem{}, fmt.Errorf("steamTransactionQuantity must be an integer number")
	}
	volume, err := volumeNumber.Int64()
	if err != nil || volume < 0 || (strconv.IntSize == 32 && volume > 1<<31-1) {
		return MarketItem{}, fmt.Errorf("invalid steamTransactionQuantity %q", volumeNumber.String())
	}

	priceFields := []struct {
		field    string
		platform string
	}{
		{field: "steamSellPrice"},
		{field: "buffSellPrice", platform: "BUFF"},
		{field: "uuypSellPrice", platform: "悠悠有品"},
		{field: "c5SellPrice", platform: "C5"},
		{field: "igxeSellPrice", platform: "IGXE"},
		{field: "ecoSellPrice", platform: "ECO"},
	}
	prices := make([]*big.Rat, 0, len(priceFields))
	steamPrice := ""
	var lowestPlatformPriceValue *big.Rat
	lowestPlatform := ""
	lowestPlatformPrice := ""
	for _, priceField := range priceFields {
		price, raw, err := parseDetailPrice(record[priceField.field], priceField.field)
		if err != nil {
			return MarketItem{}, err
		}
		if priceField.field == "steamSellPrice" {
			steamPrice = raw
		} else if price.Sign() > 0 &&
			(lowestPlatformPriceValue == nil || price.Cmp(lowestPlatformPriceValue) < 0) {
			lowestPlatformPriceValue = price
			lowestPlatform = priceField.platform
			lowestPlatformPrice = raw
		}
		prices = append(prices, price)
	}
	ratio, err := detailRatioBP(prices)
	if err != nil {
		return MarketItem{}, err
	}

	return MarketItem{
		ItemID:         itemID,
		MarketHashName: stringValue(record["hashName"]),
		NameCN:         stringValue(record["cnName"]),
		MarketData: core.MarketData{
			Ratio:            ratio,
			SteamDailyVolume: int(volume),
		},
		SteamPrice:          steamPrice,
		LowestPlatform:      lowestPlatform,
		LowestPlatformPrice: lowestPlatformPrice,
		UpdatedAt:           stringValue(record["updateTime"]),
	}, nil
}

func parseDetailPrice(value any, field string) (*big.Rat, string, error) {
	number, ok := value.(json.Number)
	if !ok {
		return nil, "", fmt.Errorf("%s must be a number", field)
	}
	raw := number.String()
	price, ok := new(big.Rat).SetString(raw)
	if !ok || price.Sign() < 0 {
		return nil, "", fmt.Errorf("invalid %s %q", field, raw)
	}
	return price, raw, nil
}

func detailRatioBP(prices []*big.Rat) (core.RatioBP, error) {
	if len(prices) == 0 {
		return 0, fmt.Errorf("detail prices are missing")
	}
	var minimum *big.Rat
	for _, price := range prices {
		if price != nil && price.Sign() > 0 && (minimum == nil || price.Cmp(minimum) < 0) {
			minimum = price
		}
	}
	if minimum == nil {
		return 0, fmt.Errorf("detail has no positive sell price")
	}

	steamNet := calculateSteamNet(prices[0])
	if steamNet.Sign() <= 0 {
		return 0, fmt.Errorf("Steam net price must be positive")
	}
	rawRatio := new(big.Rat).Quo(minimum, steamNet)
	scaled := new(big.Rat).Mul(rawRatio, big.NewRat(10000, 1))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(scaled.Num(), scaled.Denom(), remainder)
	comparison := new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(scaled.Denom())
	if comparison > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if quotient.Sign() < 0 || !quotient.IsInt64() || quotient.Int64() > maxRatioBP {
		return 0, fmt.Errorf("RatioBP is out of range")
	}
	return core.RatioBP(quotient.Int64()), nil
}

func calculateSteamNet(steamPrice *big.Rat) *big.Rat {
	if steamPrice == nil || steamPrice.Sign() <= 0 {
		return new(big.Rat)
	}
	divided := new(big.Rat).Quo(
		new(big.Rat).Mul(steamPrice, big.NewRat(100, 1)),
		big.NewRat(115, 100),
	)
	dividedCents := new(big.Int).Quo(divided.Num(), divided.Denom())
	dividedPrice := new(big.Rat).SetFrac(dividedCents, big.NewInt(100))
	minusFee := new(big.Rat).Sub(steamPrice, big.NewRat(14, 100))
	minimum := dividedPrice
	if minusFee.Cmp(minimum) < 0 {
		minimum = minusFee
	}
	if minimum.Sign() <= 0 {
		return new(big.Rat)
	}
	cents := roundPositiveRat(new(big.Rat).Mul(minimum, big.NewRat(100, 1)))
	return new(big.Rat).SetFrac(cents, big.NewInt(100))
}

func roundPositiveRat(value *big.Rat) *big.Int {
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(value.Num(), value.Denom(), remainder)
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(value.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
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
	Model                   int          `json:"model"`
	Platforms               []string     `json:"platforms"`
	AppID                   string       `json:"appid"`
	MinPrice                *json.Number `json:"minPrice"`
	MaxPrice                *json.Number `json:"maxPrice"`
	MinTransactionQuantity  *int         `json:"minTransactionQuantity"`
	MinWeekGrowthRate       *json.Number `json:"minWeekGrowthRate"`
	MaxWeekGrowthRate       *json.Number `json:"maxWeekGrowthRate"`
	MinRate                 *json.Number `json:"minRate"`
	MaxRate                 *json.Number `json:"maxRate"`
	MinSteamSellSubBuyRatio *json.Number `json:"minSteamSellSubBuyRatio"`
	TimeAfter               *string      `json:"timeAfter"`
	PageNum                 int          `json:"pageNum"`
	PageSize                int          `json:"pageSize"`
	BuffTax                 json.Number  `json:"buffTax"`
	BuffLimit               int          `json:"buffLimit"`
	UUYPTax                 json.Number  `json:"uuypTax"`
	UUYPLimit               int          `json:"uuypLimit"`
	C5Tax                   json.Number  `json:"c5Tax"`
	C5Limit                 int          `json:"c5Limit"`
	IGXETax                 json.Number  `json:"igxeTax"`
	IGXELimit               int          `json:"igxeLimit"`
	EcoTax                  json.Number  `json:"ecoTax"`
	EcoLimit                int          `json:"ecoLimit"`
}
