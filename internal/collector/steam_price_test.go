package collector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSteamPriceClientFetchesExactCNYLowestPrice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != steamPricePath {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("appid") != "730" || query.Get("currency") != "23" ||
			query.Get("market_hash_name") != "MP5-SD | Necro Jr. (Field-Tested)" {
			t.Errorf("query = %#v", query)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(response, `{"success":true,"lowest_price":"¥ 0.70","volume":"492","median_price":"¥ 0.93"}`)
	}))
	t.Cleanup(server.Close)

	client := NewSteamPriceClient(server.URL, server.Client())
	price, err := client.FetchLowestPrice(
		context.Background(),
		"MP5-SD | Necro Jr. (Field-Tested)",
	)
	if err != nil {
		t.Fatal(err)
	}
	if price != "0.70" {
		t.Fatalf("price = %q, want 0.70", price)
	}
}

func TestSteamPriceClientStopsOnAccessRestriction(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		response.Header().Set("Retry-After", "120")
		response.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(response, `{"success":false}`)
	}))
	t.Cleanup(server.Close)

	client := NewSteamPriceClient(server.URL, server.Client())
	_, err := client.FetchLowestPrice(context.Background(), "item")
	if !errors.Is(err, ErrSteamAccessRestricted) ||
		err == nil || !containsAll(err.Error(), "HTTP 429", "Retry-After=120") {
		t.Fatalf("error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one without retry", requests)
	}
}

func TestParseSteamCNYPrice(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "¥ 0.70", want: "0.70"},
		{input: "￥1", want: "1.00"},
		{input: "¥ 1,234.50", want: "1234.50"},
	}
	for _, testCase := range tests {
		got, err := parseSteamCNYPrice(testCase.input)
		if err != nil {
			t.Fatalf("parseSteamCNYPrice(%q): %v", testCase.input, err)
		}
		if got != testCase.want {
			t.Fatalf("parseSteamCNYPrice(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}

	for _, input := range []string{"", "$0.70", "¥ 0", "¥ -1.00", "¥ 1,23.00", "¥ 1.234"} {
		if _, err := parseSteamCNYPrice(input); err == nil {
			t.Errorf("parseSteamCNYPrice(%q) succeeded", input)
		}
	}
}

func TestCalculateRatioBPUsesOfficialSteamPriceExactly(t *testing.T) {
	ratio, err := CalculateRatioBP("1.15", "0.59")
	if err != nil {
		t.Fatal(err)
	}
	if ratio != 5900 {
		t.Fatalf("ratio = %d, want 5900", ratio)
	}

	ratio, err = CalculateRatioBP("0.70", "0.35")
	if err != nil {
		t.Fatal(err)
	}
	if ratio != 6250 {
		t.Fatalf("ratio = %d, want 6250", ratio)
	}
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
