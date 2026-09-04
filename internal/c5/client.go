package c5

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
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

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const (
	balancePath  = "/merchant/account/v2/balance"
	quickBuyPath = "/merchant/trade/v2/quick-buy"
	orderPath    = "/merchant/order/v2/buy/detail"
	maxBodyBytes = 1 << 20
)

var ErrAccessRestricted = errors.New("C5 access restricted")

type MoneyFen int64

func ParseMoney(value string) (MoneyFen, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("money is empty")
	}
	if strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, fmt.Errorf("money %q must be unsigned", value)
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("money %q is not a plain decimal", value)
	}
	for _, character := range parts[0] {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("money %q is not a plain decimal", value)
		}
	}
	fraction := ""
	if len(parts) == 2 {
		if parts[1] == "" {
			return 0, fmt.Errorf("money %q has an empty fraction", value)
		}
		for _, character := range parts[1] {
			if character < '0' || character > '9' {
				return 0, fmt.Errorf("money %q is not a plain decimal", value)
			}
		}
		fraction = parts[1]
	}
	for len(fraction) > 2 && strings.HasSuffix(fraction, "0") {
		fraction = strings.TrimSuffix(fraction, "0")
	}
	if len(fraction) > 2 {
		return 0, fmt.Errorf("money %q has precision smaller than one fen", value)
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("money %q is out of range", value)
	}
	fractionFen := int64(0)
	if fraction != "" {
		fractionFen, _ = strconv.ParseInt(fraction, 10, 64)
	}
	if whole > (int64(1<<63-1)-fractionFen)/100 {
		return 0, fmt.Errorf("money %q is out of range", value)
	}
	return MoneyFen(whole*100 + fractionFen), nil
}

func (money MoneyFen) String() string {
	if money < 0 {
		return "-" + MoneyFen(-money).String()
	}
	return fmt.Sprintf("%d.%02d", int64(money)/100, int64(money)%100)
}

type Client struct {
	baseURL    string
	appKey     string
	tradeURL   string
	httpClient *http.Client
}

func NewClient(baseURL, appKey, tradeURL string, httpClient *http.Client) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("C5 base URL cannot be empty")
	}
	if strings.TrimSpace(appKey) == "" {
		return nil, fmt.Errorf("C5 app key cannot be empty")
	}
	if strings.TrimSpace(tradeURL) == "" {
		return nil, fmt.Errorf("C5 trade URL cannot be empty")
	}
	if err := validateTradeURL(tradeURL); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("C5 base URL is invalid")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		appKey:     appKey,
		tradeURL:   tradeURL,
		httpClient: httpClient,
	}, nil
}

type APIError struct {
	Code    string
	Message string
}

func (err *APIError) Error() string {
	if err.Code == "" {
		return "C5 API error: " + err.Message
	}
	return fmt.Sprintf("C5 API error: code=%s message=%s", err.Code, err.Message)
}

type QuickBuyRequest struct {
	OutTradeNo     string
	MarketHashName string
	MaxPrice       string
}

type QuickBuyResult struct {
	ActualPay MoneyFen
	OrderID   string
}

type Order struct {
	ActualPay MoneyFen
	OrderID   string
}

func (client *Client) Balance(ctx context.Context) (MoneyFen, error) {
	data, err := client.do(ctx, http.MethodGet, balancePath, nil, nil)
	if err != nil {
		return 0, err
	}
	var response struct {
		MoneyAmount json.RawMessage `json:"moneyAmount"`
	}
	if err := decodeData(data, &response); err != nil {
		return 0, fmt.Errorf("decode C5 balance: %w", err)
	}
	balance, err := moneyFromJSON(response.MoneyAmount)
	if err != nil {
		return 0, fmt.Errorf("decode C5 moneyAmount: %w", err)
	}
	if balance < 0 {
		return 0, fmt.Errorf("C5 moneyAmount cannot be negative")
	}
	return balance, nil
}

func (client *Client) QuickBuy(ctx context.Context, request QuickBuyRequest) (QuickBuyResult, error) {
	if strings.TrimSpace(request.OutTradeNo) == "" {
		return QuickBuyResult{}, fmt.Errorf("C5 outTradeNo cannot be empty")
	}
	if strings.TrimSpace(request.MarketHashName) == "" {
		return QuickBuyResult{}, fmt.Errorf("C5 marketHashName cannot be empty")
	}
	maxPrice, err := ParseMoney(request.MaxPrice)
	if err != nil || maxPrice <= 0 {
		return QuickBuyResult{}, fmt.Errorf("C5 maxPrice must be positive exact money")
	}
	rawPrice := json.RawMessage(request.MaxPrice)
	if !json.Valid(rawPrice) {
		return QuickBuyResult{}, fmt.Errorf("C5 maxPrice must be a JSON decimal number")
	}
	body := struct {
		OutTradeNo     string          `json:"outTradeNo"`
		TradeURL       string          `json:"tradeUrl"`
		AppID          int             `json:"appId"`
		MarketHashName string          `json:"marketHashName"`
		MaxPrice       json.RawMessage `json:"maxPrice"`
	}{
		OutTradeNo:     request.OutTradeNo,
		TradeURL:       client.tradeURL,
		AppID:          730,
		MarketHashName: request.MarketHashName,
		MaxPrice:       rawPrice,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return QuickBuyResult{}, fmt.Errorf("encode C5 quick-buy request: %w", err)
	}
	data, err := client.do(ctx, http.MethodPost, quickBuyPath, nil, bytes.NewReader(encoded))
	if err != nil {
		return QuickBuyResult{}, err
	}
	var response struct {
		ActualPay json.RawMessage `json:"actualPay"`
		OrderID   json.RawMessage `json:"orderId"`
	}
	if err := decodeData(data, &response); err != nil {
		return QuickBuyResult{}, fmt.Errorf("decode C5 quick-buy result: %w", err)
	}
	actualPay, err := moneyFromJSON(response.ActualPay)
	if err != nil || actualPay <= 0 {
		return QuickBuyResult{}, fmt.Errorf("C5 quick-buy actualPay must be positive exact money")
	}
	if actualPay > maxPrice {
		return QuickBuyResult{}, fmt.Errorf("C5 quick-buy actualPay %s exceeds maxPrice %s", actualPay, maxPrice)
	}
	orderID, err := positiveIdentifier(response.OrderID)
	if err != nil {
		return QuickBuyResult{}, fmt.Errorf("decode C5 quick-buy orderId: %w", err)
	}
	return QuickBuyResult{ActualPay: actualPay, OrderID: orderID}, nil
}

func (client *Client) OrderByOutTradeNo(ctx context.Context, outTradeNo string) (Order, error) {
	if strings.TrimSpace(outTradeNo) == "" {
		return Order{}, fmt.Errorf("C5 outTradeNo cannot be empty")
	}
	query := url.Values{"outTradeNo": []string{outTradeNo}}
	data, err := client.do(ctx, http.MethodGet, orderPath, query, nil)
	if err != nil {
		return Order{}, err
	}
	var response struct {
		Price   json.RawMessage `json:"price"`
		OrderID json.RawMessage `json:"orderId"`
	}
	if err := decodeData(data, &response); err != nil {
		return Order{}, fmt.Errorf("decode C5 order detail: %w", err)
	}
	actualPay, err := moneyFromJSON(response.Price)
	if err != nil || actualPay <= 0 {
		return Order{}, fmt.Errorf("C5 order price must be positive exact money")
	}
	orderID, err := positiveIdentifier(response.OrderID)
	if err != nil {
		return Order{}, fmt.Errorf("decode C5 orderId: %w", err)
	}
	return Order{ActualPay: actualPay, OrderID: orderID}, nil
}

type envelope struct {
	Success      *bool           `json:"success"`
	ErrorCode    json.RawMessage `json:"errorCode"`
	ErrorCodeStr json.RawMessage `json:"errorCodeStr"`
	ErrorMsg     json.RawMessage `json:"errorMsg"`
	Data         json.RawMessage `json:"data"`
}

func (client *Client) do(
	ctx context.Context,
	method, path string,
	query url.Values,
	body io.Reader,
) (json.RawMessage, error) {
	endpoint, err := url.Parse(client.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("create C5 request for %s: invalid endpoint", path)
	}
	values := endpoint.Query()
	for key, entries := range query {
		for _, entry := range entries {
			values.Add(key, entry)
		}
	}
	values.Set("app-key", client.appKey)
	endpoint.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create C5 request for %s: %w", path, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "gzip, br, zstd, deflate")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("C5 request %s: %s", path, client.redact(err.Error()))
	}
	defer response.Body.Close()

	var reader io.Reader = response.Body
	contentEncoding := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding")))
	switch contentEncoding {
	case "":
	case "gzip":
		gzipReader, err := gzip.NewReader(response.Body)
		if err != nil {
			return nil, fmt.Errorf("decode C5 response for %s: invalid gzip", path)
		}
		defer gzipReader.Close()
		reader = gzipReader
	case "br":
		reader = brotli.NewReader(response.Body)
	case "zstd":
		zstdReader, err := zstd.NewReader(response.Body)
		if err != nil {
			return nil, fmt.Errorf("decode C5 response for %s: invalid zstd", path)
		}
		defer zstdReader.Close()
		reader = zstdReader
	case "deflate":
		deflateReader, err := zlib.NewReader(response.Body)
		if err != nil {
			return nil, fmt.Errorf("decode C5 response for %s: invalid deflate", path)
		}
		defer deflateReader.Close()
		reader = deflateReader
	default:
		return nil, fmt.Errorf(
			"decode C5 response for %s: unsupported Content-Encoding %q",
			path,
			contentEncoding,
		)
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read C5 response for %s: %w", path, err)
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("%w: empty C5 response for %s", ErrAccessRestricted, path)
	}
	if len(content) > maxBodyBytes {
		return nil, fmt.Errorf("C5 response for %s exceeds size limit", path)
	}
	trimmed := strings.TrimSpace(string(content))
	if strings.HasPrefix(strings.ToLower(trimmed), "<!doctype html") ||
		strings.HasPrefix(strings.ToLower(trimmed), "<html") {
		return nil, fmt.Errorf("%w: HTML C5 response for %s", ErrAccessRestricted, path)
	}
	if response.StatusCode == http.StatusUnauthorized ||
		response.StatusCode == http.StatusForbidden ||
		response.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w: C5 response status %d for %s", ErrAccessRestricted, response.StatusCode, path)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("C5 response status %d for %s", response.StatusCode, path)
	}

	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var value envelope
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode C5 response for %s: %w", path, err)
	}
	if value.Success == nil {
		return nil, fmt.Errorf("decode C5 response for %s: missing success", path)
	}
	if !*value.Success {
		message := rawScalar(value.ErrorMsg)
		code := rawScalar(value.ErrorCodeStr)
		if code == "" {
			code = rawScalar(value.ErrorCode)
		}
		return nil, &APIError{Code: code, Message: message}
	}
	if len(value.Data) == 0 || string(value.Data) == "null" {
		return nil, fmt.Errorf("decode C5 response for %s: missing data", path)
	}
	return value.Data, nil
}

func decodeData(data json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return nil
}

func moneyFromJSON(raw json.RawMessage) (MoneyFen, error) {
	text := rawScalar(raw)
	if text == "" {
		return 0, fmt.Errorf("missing money")
	}
	return ParseMoney(text)
}

func positiveIdentifier(raw json.RawMessage) (string, error) {
	value := rawScalar(raw)
	if value == "" {
		return "", fmt.Errorf("identifier is missing")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return "", fmt.Errorf("identifier %q is not decimal", value)
		}
	}
	positive := strings.TrimLeft(value, "0")
	if positive == "" {
		return "", fmt.Errorf("identifier must be positive")
	}
	return value, nil
}

func rawScalar(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return number.String()
	}
	return ""
}

func (client *Client) redact(value string) string {
	value = strings.ReplaceAll(value, client.appKey, "[REDACTED]")
	value = strings.ReplaceAll(value, url.QueryEscape(client.appKey), "[REDACTED]")
	return value
}
func validateTradeURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil ||
		parsed.Scheme != "https" ||
		!strings.EqualFold(parsed.Hostname(), "steamcommunity.com") ||
		parsed.Port() != "" ||
		parsed.User != nil ||
		parsed.Path != "/tradeoffer/new/" ||
		parsed.Fragment != "" {
		return fmt.Errorf("C5 trade URL must be an HTTPS Steam Community trade-offer URL")
	}
	query := parsed.Query()
	partner := query.Get("partner")
	token := query.Get("token")
	if partner == "" || token == "" {
		return fmt.Errorf("C5 trade URL must contain partner and token")
	}
	for _, character := range partner {
		if character < '0' || character > '9' {
			return fmt.Errorf("C5 trade URL partner must be a positive decimal ID")
		}
	}
	if strings.TrimLeft(partner, "0") == "" {
		return fmt.Errorf("C5 trade URL partner must be a positive decimal ID")
	}
	if len(query) != 2 {
		return fmt.Errorf("C5 trade URL contains unexpected query parameters")
	}
	return nil
}
