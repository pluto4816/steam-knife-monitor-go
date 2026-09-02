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

	page, err := client.FetchCandidatePage(context.Background(), 3)
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
	if page.RawCount != 1 || page.PageNum != 3 || page.PageSize != SMISPageSize ||
		len(page.Items) != 1 || page.Items[0].ItemID != 53 || page.Items[0].NameCN != "AK-47 | 精英之作 (久经沙场)" {
		t.Fatalf("unexpected candidate page: %#v", page)
	}
}

func TestFetchCandidatePageEmptyItems(t *testing.T) {
	client := testClient(t, func(response http.ResponseWriter, _ *http.Request) {
		writeResponse(response, http.StatusOK, "application/json", `{"code":200,"message":"success","data":{"items":[]}}`)
	})
	page, err := client.FetchCandidatePage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.RawCount != 0 || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("page = %#v, want zero raw count and non-nil empty items", page)
	}
}

func TestFetchMarketItemVerifiedRatios(t *testing.T) {
	tests := []struct {
		name              string
		itemID            int64
		steamPrice        string
		buffPrice         string
		uuypPrice         string
		c5Price           string
		igxePrice         string
		ecoPrice          string
		wantRatio         core.RatioBP
		wantPlatform      string
		wantPlatformPrice string
	}{
		{
			name: "72825", itemID: 72825, steamPrice: "1157.77",
			buffPrice: "22.9", uuypPrice: "0", c5Price: "0",
			igxePrice: "0", ecoPrice: "0", wantRatio: 227,
			wantPlatform: "BUFF", wantPlatformPrice: "22.9",
		},
		{
			name: "76026", itemID: 76026, steamPrice: "12600.0",
			buffPrice: "9.87", uuypPrice: "8.9800", c5Price: "10.0",
			igxePrice: "0", ecoPrice: "0", wantRatio: 8,
			wantPlatform: "悠悠有品", wantPlatformPrice: "8.9800",
		},
		{
			name: "72299", itemID: 72299, steamPrice: "5433.32",
			buffPrice: "148.0", uuypPrice: "200.0", c5Price: "100.78",
			igxePrice: "0", ecoPrice: "0", wantRatio: 213,
			wantPlatform: "C5", wantPlatformPrice: "100.78",
		},
		{
			name: "rounds_not_truncates_68632", itemID: 68632, steamPrice: "346.55",
			buffPrice: "3.52", uuypPrice: "2.78", c5Price: "3.1",
			igxePrice: "0", ecoPrice: "2.38", wantRatio: 79,
			wantPlatform: "ECO", wantPlatformPrice: "2.38",
		},
		{
			name: "igxe_exact_price", itemID: 53, steamPrice: "11.50",
			buffPrice: "6.0", uuypPrice: "5.5", c5Price: "5.2",
			igxePrice: "4.7500", ecoPrice: "5.1", wantRatio: 4750,
			wantPlatform: "IGXE", wantPlatformPrice: "4.7500",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var method, path, authHeader, auth2Header string
			client := testClient(t, func(response http.ResponseWriter, request *http.Request) {
				method = request.Method
				path = request.URL.Path
				authHeader = request.Header.Get("Auth")
				auth2Header = request.Header.Get("Auth2")
				body := fmt.Sprintf(`{
					"code":200,"message":"success","data":{
						"id":%d,
						"hashName":"Market Hash %d",
						"cnName":"中文名 %d",
						"steamSellPrice":%s,
						"steamTransactionQuantity":344,
						"buffSellPrice":%s,
						"uuypSellPrice":%s,
						"c5SellPrice":%s,
						"igxeSellPrice":%s,
						"ecoSellPrice":%s,
						"updateTime":"2026-08-19 12:00:00"
					}}`,
					testCase.itemID, testCase.itemID, testCase.itemID,
					testCase.steamPrice, testCase.buffPrice, testCase.uuypPrice,
					testCase.c5Price, testCase.igxePrice, testCase.ecoPrice,
				)
				writeResponse(response, http.StatusOK, "application/json", body)
			})

			item, err := client.FetchMarketItem(context.Background(), testCase.itemID)
			if err != nil {
				t.Fatal(err)
			}
			if method != http.MethodGet || path != fmt.Sprintf(detailPath, testCase.itemID) {
				t.Fatalf("request = %s %s, want GET detail path", method, path)
			}
			if authHeader == "" || auth2Header != auth2 {
				t.Fatalf("Auth headers missing or incorrect: Auth=%t Auth2=%q", authHeader != "", auth2Header)
			}
			if item.ItemID != testCase.itemID ||
				item.MarketHashName != fmt.Sprintf("Market Hash %d", testCase.itemID) ||
				item.NameCN != fmt.Sprintf("中文名 %d", testCase.itemID) {
				t.Fatalf("unexpected identity: %#v", item)
			}
			if item.MarketData.Ratio != testCase.wantRatio {
				t.Fatalf("RatioBP = %d, want %d", item.MarketData.Ratio, testCase.wantRatio)
			}
			if item.MarketData.SteamDailyVolume != 344 {
				t.Fatalf("volume = %d, want 344", item.MarketData.SteamDailyVolume)
			}
			if item.SteamPrice != testCase.steamPrice {
				t.Fatalf("SteamPrice = %q, want exact JSON text %q", item.SteamPrice, testCase.steamPrice)
			}
			if item.LowestPlatform != testCase.wantPlatform ||
				item.LowestPlatformPrice != testCase.wantPlatformPrice {
				t.Fatalf(
					"lowest platform = %q price=%q, want %q price=%q",
					item.LowestPlatform, item.LowestPlatformPrice,
					testCase.wantPlatform, testCase.wantPlatformPrice,
				)
			}
			if item.UpdatedAt != "2026-08-19 12:00:00" {
				t.Fatalf("UpdatedAt = %q", item.UpdatedAt)
			}
		})
	}
}

func TestFetchMarketItemLowestPlatformTieUsesDisplayOrder(t *testing.T) {
	client := detailResponseClient(t, `{
		"id":53,"hashName":"Tie","cnName":"Tie",
		"steamSellPrice":10.00,"steamTransactionQuantity":350,
		"buffSellPrice":5.000,"uuypSellPrice":5.00,"c5SellPrice":5.0,
		"igxeSellPrice":0,"ecoSellPrice":0,"updateTime":"2026-08-19 12:00:00"
	}`)
	item, err := client.FetchMarketItem(context.Background(), 53)
	if err != nil {
		t.Fatal(err)
	}
	if item.LowestPlatform != "BUFF" || item.LowestPlatformPrice != "5.000" {
		t.Fatalf(
			"lowest platform = %q price=%q, want BUFF with exact price 5.000",
			item.LowestPlatform,
			item.LowestPlatformPrice,
		)
	}
}

func TestFetchMarketItemRoundsExactRatioBPTieDown(t *testing.T) {
	client := detailResponseClient(t, `{
		"id":53,"hashName":"Tie","cnName":"Tie",
		"steamSellPrice":1.15,"steamTransactionQuantity":1,
		"buffSellPrice":0.12345,"uuypSellPrice":0,"c5SellPrice":0,
		"igxeSellPrice":0,"ecoSellPrice":0,"updateTime":"2026-08-19 12:00:00"
	}`)
	item, err := client.FetchMarketItem(context.Background(), 53)
	if err != nil {
		t.Fatal(err)
	}
	if item.MarketData.Ratio != core.RatioBP(1234) {
		t.Fatalf("RatioBP = %d, want exact tie rounded down to 1234", item.MarketData.Ratio)
	}
}

func TestFetchMarketItemRejectsInvalidDetail(t *testing.T) {
	tests := []struct {
		name      string
		requestID int64
		record    string
		wantError string
	}{
		{
			name: "response ID mismatch", requestID: 53, wantError: "does not match",
			record: `{"id":54,"steamSellPrice":1.15,"steamTransactionQuantity":1,
				"buffSellPrice":1,"uuypSellPrice":1,"c5SellPrice":1,"igxeSellPrice":0,"ecoSellPrice":0}`,
		},
		{
			name: "negative volume", requestID: 53, wantError: "steamTransactionQuantity",
			record: `{"id":53,"steamSellPrice":1.15,"steamTransactionQuantity":-1,
				"buffSellPrice":1,"uuypSellPrice":1,"c5SellPrice":1,"igxeSellPrice":0,"ecoSellPrice":0}`,
		},
		{
			name: "fractional volume", requestID: 53, wantError: "steamTransactionQuantity",
			record: `{"id":53,"steamSellPrice":1.15,"steamTransactionQuantity":1.5,
				"buffSellPrice":1,"uuypSellPrice":1,"c5SellPrice":1,"igxeSellPrice":0,"ecoSellPrice":0}`,
		},
		{
			name: "missing volume", requestID: 53, wantError: "steamTransactionQuantity",
			record: `{"id":53,"steamSellPrice":1.15,
				"buffSellPrice":1,"uuypSellPrice":1,"c5SellPrice":1,"igxeSellPrice":0,"ecoSellPrice":0}`,
		},
		{
			name: "Steam net not positive", requestID: 53, wantError: "Steam net price",
			record: `{"id":53,"steamSellPrice":0,"steamTransactionQuantity":1,
				"buffSellPrice":1,"uuypSellPrice":1,"c5SellPrice":1,"igxeSellPrice":0,"ecoSellPrice":0}`,
		},
		{
			name: "no positive prices", requestID: 53, wantError: "no positive sell price",
			record: `{"id":53,"steamSellPrice":0,"steamTransactionQuantity":1,
				"buffSellPrice":0,"uuypSellPrice":0,"c5SellPrice":0,"igxeSellPrice":0,"ecoSellPrice":0}`,
		},
		{
			name: "malformed price", requestID: 53, wantError: "buffSellPrice must be a number",
			record: `{"id":53,"steamSellPrice":1.15,"steamTransactionQuantity":1,
				"buffSellPrice":"1","uuypSellPrice":1,"c5SellPrice":1,"igxeSellPrice":0,"ecoSellPrice":0}`,
		},
		{
			name: "missing price", requestID: 53, wantError: "ecoSellPrice must be a number",
			record: `{"id":53,"steamSellPrice":1.15,"steamTransactionQuantity":1,
				"buffSellPrice":1,"uuypSellPrice":1,"c5SellPrice":1,"igxeSellPrice":0}`,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client := detailResponseClient(t, testCase.record)
			_, err := client.FetchMarketItem(context.Background(), testCase.requestID)
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("error = %v, want substring %q", err, testCase.wantError)
			}
		})
	}
}

func TestFetchMarketItemRejectsNonpositiveRequestIDWithoutRequest(t *testing.T) {
	client := NewSMISClient("http://unused.invalid", nil)
	if _, err := client.FetchMarketItem(context.Background(), 0); err == nil {
		t.Fatal("item ID zero was accepted")
	}
}

func TestFetchMarketItemReusesRestrictionHandling(t *testing.T) {
	attempts := 0
	client := testClient(t, func(response http.ResponseWriter, _ *http.Request) {
		attempts++
		response.WriteHeader(http.StatusForbidden)
	})
	_, err := client.FetchMarketItem(context.Background(), 53)
	if !errors.Is(err, ErrAccessRestricted) {
		t.Fatalf("error = %v, want ErrAccessRestricted", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want one request and no retry", attempts)
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

	page, err := client.FetchExchangePage(context.Background(), 4)
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
	for _, key := range []string{
		"minPrice", "maxPrice", "minTransactionQuantity", "minWeekGrowthRate",
		"maxWeekGrowthRate", "minRate", "maxRate", "minSteamSellSubBuyRatio", "timeAfter",
	} {
		value, exists := body[key]
		if !exists || value != nil {
			t.Fatalf("neutral filter %s = %#v, exists=%t; want present JSON null", key, value, exists)
		}
	}
	for key, want := range map[string]string{
		"buffTax":   "0.015",
		"buffLimit": "9999999", "uuypTax": "0.01", "uuypLimit": "9999999",
		"c5Tax": "0.01", "c5Limit": "100", "igxeTax": "0.006",
		"igxeLimit": "9999999", "ecoTax": "0", "ecoLimit": "9999999",
	} {
		assertNumber(t, body[key], want)
	}
	if page.RawCount != 1 || len(page.Items) != 1 {
		t.Fatalf("unexpected exchange page: %#v", page)
	}
	item := page.Items[0]
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
	if item.UpdatedAt != "2026-08-18 16:23:18" {
		t.Fatalf("UpdatedAt = %q", item.UpdatedAt)
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
			page, err := client.FetchExchangePage(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if page.RawCount != 2 || len(page.Items) != 1 || page.Items[0].ItemID != 9 {
				t.Fatalf("invalid record handling = %#v, want raw=2 valid=1", page)
			}
		})
	}
}

func TestMissingSteamPriceDoesNotInvalidateMarketData(t *testing.T) {
	client := testClient(t, func(response http.ResponseWriter, _ *http.Request) {
		writeResponse(response, http.StatusOK, "application/json", `{"code":200,"data":[{"id":1,"extremeRatio":0.6,"steamTransactionQuantity":350}]}`)
	})
	page, err := client.FetchExchangePage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].SteamPrice != "" {
		t.Fatalf("unexpected page: %#v", page)
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

func detailResponseClient(t *testing.T, record string) *SMISClient {
	t.Helper()
	return testClient(t, func(response http.ResponseWriter, _ *http.Request) {
		writeResponse(response, http.StatusOK, "application/json",
			`{"code":200,"message":"success","data":`+record+`}`)
	})
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
