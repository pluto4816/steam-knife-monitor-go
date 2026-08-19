package collector

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

func TestAuthRoundTrip(t *testing.T) {
	timestamp := "1787135729123"
	encoded, err := makeAuth(timestamp)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("Auth is not Base64: %v", err)
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		t.Fatalf("ciphertext length %d is not a positive block multiple", len(ciphertext))
	}
	block, err := aes.NewCipher([]byte(authKey))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, []byte(authIV)).CryptBlocks(plaintext, ciphertext)
	paddingLength := int(plaintext[len(plaintext)-1])
	if paddingLength < 1 || paddingLength > aes.BlockSize {
		t.Fatalf("invalid PKCS#7 padding length %d", paddingLength)
	}
	for _, value := range plaintext[len(plaintext)-paddingLength:] {
		if int(value) != paddingLength {
			t.Fatalf("invalid PKCS#7 padding byte %d", value)
		}
	}
	if got := string(plaintext[:len(plaintext)-paddingLength]); got != timestamp {
		t.Fatalf("decrypted Auth = %q, want %q", got, timestamp)
	}
}

func TestFetchCandidatePageRequestAndResponse(t *testing.T) {
	var method, path, contentType, authHeader, auth2Header string
	var body map[string]any
	client := testClient(t, func(response http.ResponseWriter, request *http.Request) {
		method = request.Method
		path = request.URL.Path
		contentType = request.Header.Get("Content-Type")
		authHeader = request.Header.Get("Auth")
		auth2Header = request.Header.Get("Auth2")
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeResponse(response, http.StatusOK, "application/json", `{
			"code":200,"message":"success","data":{"items":[
				{"id":53,"cnName":"AK-47 | 精英之作 (久经沙场)","rarity":"军规级"}
			],"pageNum":3,"pageSize":50,"total":null}}
		`)
	})

	items, err := client.FetchCandidatePage(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != searchPath {
		t.Fatalf("request = %s %s, want POST %s", method, path, searchPath)
	}
	if contentType != "application/json" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	if authHeader == "" || auth2Header != auth2 {
		t.Fatalf("Auth headers missing or incorrect: Auth=%t Auth2=%q", authHeader != "", auth2Header)
	}
	assertNumber(t, body["pageNum"], "3")
	assertNumber(t, body["pageSize"], "50")
	assertString(t, body["game"], "csgo")
	assertString(t, body["name"], "")
	assertStringList(t, body["rarityList"], []string{"军规级"})
	assertStringList(t, body["qualityList"], []string{"普通"})
	assertStringList(t, body["exteriorList"], []string{"久经沙场"})
	for _, key := range []string{"typeList", "weaponList", "heroList", "slotList"} {
		assertStringList(t, body[key], []string{})
	}
	if len(items) != 1 || items[0].ItemID != 53 || items[0].NameCN != "AK-47 | 精英之作 (久经沙场)" {
		t.Fatalf("unexpected candidates: %#v", items)
	}
}

func TestFetchCandidatePageEmptyItems(t *testing.T) {
	client := testClient(t, func(response http.ResponseWriter, _ *http.Request) {
		writeResponse(response, http.StatusOK, "application/json", `{"code":200,"message":"success","data":{"items":[]}}`)
	})
	items, err := client.FetchCandidatePage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("items = %#v, want non-nil empty slice", items)
	}
}

func TestFetchExchangePageRequestAndExactParsing(t *testing.T) {
	var method, path string
	var body map[string]any
	client := testClient(t, func(response http.ResponseWriter, request *http.Request) {
		method = request.Method
		path = request.URL.Path
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeResponse(response, http.StatusOK, "application/json", `{
			"code":200,"message":"success","data":[{
				"id":7,
				"hashName":"1st Lieutenant Farlow | SWAT",
				"cnName":"第一中尉法洛 | 特警",
				"extremeRatio":0.6571,
				"steamTransactionQuantity":344,
				"steamSellPrice":91.0000,
				"platform":"uuyp",
				"updateTime":"2026-08-18 16:23:18"
			}]}
		`)
	})

	items, err := client.FetchExchangePage(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != exchangePath {
		t.Fatalf("request = %s %s, want POST %s", method, path, exchangePath)
	}
	assertNumber(t, body["model"], "0")
	assertString(t, body["appid"], "730")
	assertNumber(t, body["pageNum"], "4")
	assertNumber(t, body["pageSize"], "50")
	assertStringList(t, body["platforms"], []string{"buff", "uuyp", "c5", "igxe", "eco"})
	for key, want := range map[string]string{
		"minPrice": "30", "maxPrice": "5000", "minTransactionQuantity": "50",
		"minWeekGrowthRate": "-0.1", "maxWeekGrowthRate": "0.1", "minRate": "0",
		"maxRate": "1.5", "minSteamSellSubBuyRatio": "0.3", "buffTax": "0.015",
		"buffLimit": "9999999", "uuypTax": "0.01", "uuypLimit": "9999999",
		"c5Tax": "0.01", "c5Limit": "100", "igxeTax": "0.006",
		"igxeLimit": "9999999", "ecoTax": "0", "ecoLimit": "9999999",
	} {
		assertNumber(t, body[key], want)
	}
	if body["timeAfter"] != nil {
		t.Fatalf("timeAfter = %#v, want null", body["timeAfter"])
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	item := items[0]
	if item.ItemID != 7 || item.MarketHashName != "1st Lieutenant Farlow | SWAT" || item.NameCN != "第一中尉法洛 | 特警" {
		t.Fatalf("unexpected identity fields: %#v", item)
	}
	if item.MarketData.Ratio != core.RatioBP(6571) {
		t.Fatalf("ratio = %d, want 6571", item.MarketData.Ratio)
	}
	if item.MarketData.SteamDailyVolume != 344 {
		t.Fatalf("volume = %d, want 344", item.MarketData.SteamDailyVolume)
	}
	if item.SteamPrice != "91.0000" {
		t.Fatalf("SteamPrice = %q, want exact JSON text", item.SteamPrice)
	}
}

func TestExchangeSkipsInvalidOrPartialRecords(t *testing.T) {
	tests := []struct {
		name   string
		record string
	}{
		{"missing ratio", `{"id":1,"steamTransactionQuantity":344}`},
		{"null ratio", `{"id":1,"extremeRatio":null,"steamTransactionQuantity":344}`},
		{"string ratio", `{"id":1,"extremeRatio":"0.6571","steamTransactionQuantity":344}`},
		{"invalid ratio precision", `{"id":1,"extremeRatio":0.65719,"steamTransactionQuantity":344}`},
		{"missing volume", `{"id":1,"extremeRatio":0.6571}`},
		{"null volume", `{"id":1,"extremeRatio":0.6571,"steamTransactionQuantity":null}`},
		{"string volume", `{"id":1,"extremeRatio":0.6571,"steamTransactionQuantity":"344"}`},
		{"fractional volume", `{"id":1,"extremeRatio":0.6571,"steamTransactionQuantity":344.5}`},
		{"negative volume", `{"id":1,"extremeRatio":0.6571,"steamTransactionQuantity":-1}`},
		{"volume out of range", `{"id":1,"extremeRatio":0.6571,"steamTransactionQuantity":9223372036854775808}`},
		{"missing id", `{"extremeRatio":0.6571,"steamTransactionQuantity":344}`},
		{"null id", `{"id":null,"extremeRatio":0.6571,"steamTransactionQuantity":344}`},
		{"string id", `{"id":"1","extremeRatio":0.6571,"steamTransactionQuantity":344}`},
		{"zero id", `{"id":0,"extremeRatio":0.6571,"steamTransactionQuantity":344}`},
	}
	valid := `{"id":9,"extremeRatio":0.6000,"steamTransactionQuantity":350}`
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client := testClient(t, func(response http.ResponseWriter, _ *http.Request) {
				body := `{"code":200,"message":"success","data":[` + testCase.record + `,` + valid + `]}`
				writeResponse(response, http.StatusOK, "application/json", body)
			})
			items, err := client.FetchExchangePage(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].ItemID != 9 {
				t.Fatalf("invalid record was not skipped: %#v", items)
			}
		})
	}
}

func TestMissingSteamPriceDoesNotInvalidateMarketData(t *testing.T) {
	client := testClient(t, func(response http.ResponseWriter, _ *http.Request) {
		writeResponse(response, http.StatusOK, "application/json", `{"code":200,"data":[{"id":1,"extremeRatio":0.6,"steamTransactionQuantity":350}]}`)
	})
	items, err := client.FetchExchangePage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SteamPrice != "" {
		t.Fatalf("unexpected items: %#v", items)
	}
}

func TestRestrictedStatusesStopWithoutRetry(t *testing.T) {
	tests := []struct {
		status     int
		retryAfter string
	}{
		{http.StatusUnauthorized, ""},
		{http.StatusForbidden, ""},
		{http.StatusTooManyRequests, ""},
		{http.StatusTooManyRequests, "120"},
	}
	for _, testCase := range tests {
		name := fmt.Sprintf("status_%d_retry_%s", testCase.status, testCase.retryAfter)
		t.Run(name, func(t *testing.T) {
			attempts := 0
			client := testClient(t, func(response http.ResponseWriter, _ *http.Request) {
				attempts++
				if testCase.retryAfter != "" {
					response.Header().Set("Retry-After", testCase.retryAfter)
				}
				response.WriteHeader(testCase.status)
			})
			_, err := client.FetchExchangePage(context.Background(), 1)
			if !errors.Is(err, ErrAccessRestricted) {
				t.Fatalf("error = %v, want ErrAccessRestricted", err)
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
			if testCase.retryAfter != "" && !strings.Contains(err.Error(), "Retry-After=120") {
				t.Fatalf("error does not include Retry-After: %v", err)
			}
		})
	}
}

func TestChallengeHTMLIsRestricted(t *testing.T) {
	for _, marker := range []string{"Cloudflare", "CAPTCHA", "challenge"} {
		t.Run(marker, func(t *testing.T) {
			client := testClient(t, func(response http.ResponseWriter, _ *http.Request) {
				writeResponse(response, http.StatusOK, "text/html; charset=utf-8", "<html>"+marker+"</html>")
			})
			_, err := client.FetchCandidatePage(context.Background(), 1)
			if !errors.Is(err, ErrAccessRestricted) {
				t.Fatalf("error = %v, want ErrAccessRestricted", err)
			}
		})
	}
}

func TestResponseErrors(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{"HTTP 500", http.StatusInternalServerError, "application/json", `{}`},
		{"malformed JSON", http.StatusOK, "application/json", `{"code":`},
		{"failed envelope", http.StatusOK, "application/json", `{"code":500,"message":"failed","data":[]}`},
		{"missing data", http.StatusOK, "application/json", `{"code":200,"message":"success"}`},
		{"wrong exchange data shape", http.StatusOK, "application/json", `{"code":200,"data":{}}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client := testClient(t, func(response http.ResponseWriter, _ *http.Request) {
				writeResponse(response, testCase.status, testCase.contentType, testCase.body)
			})
			if _, err := client.FetchExchangePage(context.Background(), 1); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestPagesStartAtOne(t *testing.T) {
	client := NewSMISClient("http://unused.invalid", nil)
	if _, err := client.FetchCandidatePage(context.Background(), 0); err == nil {
		t.Error("candidate page zero was accepted")
	}
	if _, err := client.FetchExchangePage(context.Background(), -1); err == nil {
		t.Error("negative exchange page was accepted")
	}
}

func testClient(t *testing.T, handler http.HandlerFunc) *SMISClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewSMISClient(server.URL, server.Client())
}

func writeResponse(response http.ResponseWriter, status int, contentType, body string) {
	response.Header().Set("Content-Type", contentType)
	response.WriteHeader(status)
	_, _ = io.WriteString(response, body)
}

func assertNumber(t *testing.T, value any, want string) {
	t.Helper()
	number, ok := value.(json.Number)
	if !ok || number.String() != want {
		t.Fatalf("number = %#v, want json.Number(%q)", value, want)
	}
}

func assertString(t *testing.T, value any, want string) {
	t.Helper()
	got, ok := value.(string)
	if !ok || got != want {
		t.Fatalf("string = %#v, want %q", value, want)
	}
}

func assertStringList(t *testing.T, value any, want []string) {
	t.Helper()
	raw, ok := value.([]any)
	if !ok || len(raw) != len(want) {
		t.Fatalf("list = %#v, want %#v", value, want)
	}
	for index := range want {
		if raw[index] != want[index] {
			t.Fatalf("list = %#v, want %#v", value, want)
		}
	}
}
