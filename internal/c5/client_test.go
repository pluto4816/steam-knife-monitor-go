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
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const testTradeURL = "https://steamcommunity.com/tradeoffer/new/?partner=1&token=x"

func TestNewClientRejectsUnsafeTradeURL(t *testing.T) {
	for _, tradeURL := range []string{
		"http://steamcommunity.com/tradeoffer/new/?partner=1&token=x",
		"https://example.com/tradeoffer/new/?partner=1&token=x",
		"https://steamcommunity.com/tradeoffer/new/?partner=0&token=x",
		"https://steamcommunity.com/tradeoffer/new/?partner=1",
		"https://steamcommunity.com/tradeoffer/new/?partner=1&token=x&extra=y",
	} {
		if _, err := NewClient("https://openapi.c5game.com", "key", tradeURL, nil); err == nil {
			t.Errorf("unsafe trade URL %q was accepted", tradeURL)
		}
	}
}

func TestParseMoneyExactFen(t *testing.T) {
	tests := map[string]MoneyFen{
		"0":       0,
		"0.01":    1,
		"1":       100,
		"1.1":     110,
		"1.0000":  100,
		"123.45":  12345,
		"0001.20": 120,
	}
	for input, want := range tests {
		got, err := ParseMoney(input)
		if err != nil || got != want {
			t.Errorf("ParseMoney(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"", "-1", "+1", ".1", "1.", "1.001", "1e2", "92233720368547759"} {
		if _, err := ParseMoney(input); err == nil {
			t.Errorf("ParseMoney(%q) unexpectedly succeeded", input)
		}
	}
	if got := MoneyFen(101).String(); got != "1.01" {
		t.Fatalf("MoneyFen.String() = %q", got)
	}
}

func TestClientBalanceQuickBuyAndOrderDetail(t *testing.T) {
	const key = "top-secret-key"
	var quickBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("app-key") != key {
			t.Errorf("app-key = %q", request.URL.Query().Get("app-key"))
		}
		if request.Header.Get("Accept-Encoding") != "gzip, br, zstd, deflate" {
			t.Errorf("Accept-Encoding = %q", request.Header.Get("Accept-Encoding"))
		}
		switch request.URL.Path {
		case balancePath:
			fmt.Fprint(response, `{"success":true,"data":{"moneyAmount":10.00,"depositAmount":1}}`)
		case quickBuyPath:
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&quickBody); err != nil {
				t.Errorf("decode quick-buy body: %v", err)
			}
			fmt.Fprint(response, `{"success":true,"data":{"actualPay":0.57,"orderId":123,"delivery":1}}`)
		case orderPath:
			if request.URL.Query().Get("outTradeNo") != "trade-1" {
				t.Errorf("outTradeNo = %q", request.URL.Query().Get("outTradeNo"))
			}
			fmt.Fprint(response, `{"success":true,"data":{"price":"0.57","orderId":"123","status":1}}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, key, "https://steamcommunity.com/tradeoffer/new/?partner=1&token=x", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	balance, err := client.Balance(context.Background())
	if err != nil || balance != 1000 {
		t.Fatalf("Balance() = %s, %v", balance, err)
	}
	bought, err := client.QuickBuy(context.Background(), QuickBuyRequest{
		OutTradeNo: "trade-1", MarketHashName: "Test Skin", MaxPrice: "0.5700",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bought != (QuickBuyResult{ActualPay: 57, OrderID: "123"}) {
		t.Fatalf("quick-buy result = %#v", bought)
	}
	for _, field := range []string{"device", "itemId", "delivery", "lowPrice"} {
		if _, exists := quickBody[field]; exists {
			t.Errorf("optional quick-buy field %q was sent", field)
		}
	}
	if quickBody["outTradeNo"] != "trade-1" ||
		quickBody["marketHashName"] != "Test Skin" ||
		quickBody["tradeUrl"] == "" {
		t.Fatalf("quick-buy body = %#v", quickBody)
	}
	if number, ok := quickBody["maxPrice"].(json.Number); !ok || number.String() != "0.5700" {
		t.Fatalf("maxPrice = %#v, want exact 0.5700 number", quickBody["maxPrice"])
	}
	order, err := client.OrderByOutTradeNo(context.Background(), "trade-1")
	if err != nil || order != (Order{ActualPay: 57, OrderID: "123"}) {
		t.Fatalf("order = %#v, err = %v", order, err)
	}
}

func TestClientApplicationErrorAndAccessRestriction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == balancePath {
			fmt.Fprint(response, `{"success":false,"errorCode":1001,"errorCodeStr":"NO_MONEY","errorMsg":"insufficient"}`)
			return
		}
		response.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(response, "{}")
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "secret", testTradeURL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Balance(context.Background())
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Code != "NO_MONEY" || apiError.Message != "insufficient" {
		t.Fatalf("balance error = %#v", err)
	}
	_, err = client.OrderByOutTradeNo(context.Background(), "trade")
	if !errors.Is(err, ErrAccessRestricted) {
		t.Fatalf("restriction error = %v", err)
	}
}

func TestClientRedactsAppKeyFromTransportError(t *testing.T) {
	const key = "key with spaces"
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("failed URL %s", request.URL.String())
	})}
	client, err := NewClient("https://openapi.invalid", key, testTradeURL, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Balance(context.Background())
	if err == nil {
		t.Fatal("Balance() unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "key+with+spaces") {
		t.Fatalf("error leaked app-key: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error was not redacted: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestClientRejectsEmptyResponse(t *testing.T) {
	client := clientForHandler(t, func(response http.ResponseWriter, _ *http.Request) {})
	_, err := client.Balance(context.Background())
	if !errors.Is(err, ErrAccessRestricted) {
		t.Fatalf("error = %v, want access restriction", err)
	}
}

func TestClientDecodesSupportedContentEncodings(t *testing.T) {
	const content = `{"success":true,"data":{"moneyAmount":10.00}}`
	for _, encoding := range []string{"gzip", "br", "zstd", "deflate"} {
		t.Run(encoding, func(t *testing.T) {
			encoded := encodeTestBody(t, encoding, []byte(content))
			client := clientForHandler(t, func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Encoding", encoding)
				_, _ = response.Write(encoded)
			})
			balance, err := client.Balance(context.Background())
			if err != nil || balance != 1000 {
				t.Fatalf("Balance() = %s, %v", balance, err)
			}
		})
	}
}

func TestClientRejectsUnsupportedContentEncoding(t *testing.T) {
	client := clientForHandler(t, func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Encoding", "snappy")
		_, _ = fmt.Fprint(response, "compressed")
	})
	_, err := client.Balance(context.Background())
	if err == nil || !strings.Contains(err.Error(), `unsupported Content-Encoding "snappy"`) {
		t.Fatalf("error = %v", err)
	}
}

func encodeTestBody(t *testing.T, encoding string, content []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	var writer io.WriteCloser
	var err error
	switch encoding {
	case "gzip":
		writer = gzip.NewWriter(&buffer)
	case "br":
		writer = brotli.NewWriter(&buffer)
	case "zstd":
		writer, err = zstd.NewWriter(&buffer)
	case "deflate":
		writer = zlib.NewWriter(&buffer)
	default:
		t.Fatalf("unsupported test encoding %q", encoding)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func clientForHandler(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "key", testTradeURL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client
}
