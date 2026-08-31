package collector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFetchSteamCandidatePageRequestAndResponse(t *testing.T) {
	var gotQuery url.Values
	var gotAccept, gotUserAgent string
	client := testSteamClient(t, func(response http.ResponseWriter, request *http.Request) {
		gotQuery = request.URL.Query()
		gotAccept = request.Header.Get("Accept")
		gotUserAgent = request.Header.Get("User-Agent")
		writeSteamResponse(response, http.StatusOK, `{
			"success":true,"start":100,"pagesize":2,"total_count":426,"results":[
				{"hash_name":"AK-47 | One (Field-Tested)","asset_description":{"appid":730,"type":"Mil-Spec Grade Rifle","market_hash_name":"AK-47 | One (Field-Tested)"}},
				{"hash_name":"P250 | Two (Field-Tested)","asset_description":{"appid":730,"type":"Mil-Spec Grade Pistol","market_hash_name":"P250 | Two (Field-Tested)"}}
			]}`)
	})

	page, err := client.FetchCandidatePage(context.Background(), 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	if page.Start != 100 || page.PageSize != 2 || page.TotalCount != 426 || len(page.Items) != 2 {
		t.Fatalf("page = %#v", page)
	}
	if page.Items[0].MarketHashName != "AK-47 | One (Field-Tested)" {
		t.Fatalf("item = %#v", page.Items[0])
	}
	wantQuery := map[string]string{
		"start": "100", "count": "2", "appid": "730", "norender": "1", "l": "english",
		"category_730_Quality[]":  "tag_normal",
		"category_730_Exterior[]": "tag_WearCategory2",
		"category_730_Rarity[]":   "tag_Rarity_Rare_Weapon",
	}
	for key, want := range wantQuery {
		if gotQuery.Get(key) != want {
			t.Fatalf("query %s = %q, want %q", key, gotQuery.Get(key), want)
		}
	}
	if gotAccept != "application/json" || gotUserAgent != steamUserAgent {
		t.Fatalf("headers Accept=%q User-Agent=%q", gotAccept, gotUserAgent)
	}
}

func TestFetchSteamCandidatePageStopsOnRestrictionsAndEmptyBody(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{"forbidden", http.StatusForbidden, "application/json", `{}`},
		{"rate limited", http.StatusTooManyRequests, "application/json", `{}`},
		{"empty success", http.StatusOK, "application/json", "  \n"},
		{"challenge", http.StatusOK, "text/html", `<html><title>Just a moment...</title><body>cf-chl</body></html>`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client := testSteamClient(t, func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", testCase.contentType)
				response.WriteHeader(testCase.status)
				_, _ = fmt.Fprint(response, testCase.body)
			})
			_, err := client.FetchCandidatePage(context.Background(), 0, 10)
			if !errors.Is(err, ErrSteamAccessRestricted) {
				t.Fatalf("error = %v, want ErrSteamAccessRestricted", err)
			}
		})
	}
}

func TestFetchSteamCandidatePageRejectsMismatchedFilters(t *testing.T) {
	tests := []struct {
		name   string
		result string
		want   string
	}{
		{"wrong app", `{"hash_name":"AK-47 | One (Field-Tested)","asset_description":{"appid":570,"type":"Mil-Spec Grade Rifle","market_hash_name":"AK-47 | One (Field-Tested)"}}`, "appid"},
		{"wrong rarity", `{"hash_name":"AK-47 | One (Field-Tested)","asset_description":{"appid":730,"type":"Restricted Rifle","market_hash_name":"AK-47 | One (Field-Tested)"}}`, "type"},
		{"wrong exterior", `{"hash_name":"AK-47 | One (Minimal Wear)","asset_description":{"appid":730,"type":"Mil-Spec Grade Rifle","market_hash_name":"AK-47 | One (Minimal Wear)"}}`, "Field-Tested"},
		{"wrong quality", `{"hash_name":"StatTrak™ AK-47 | One (Field-Tested)","asset_description":{"appid":730,"type":"Mil-Spec Grade Rifle","market_hash_name":"StatTrak™ AK-47 | One (Field-Tested)"}}`, "Normal"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client := testSteamClient(t, func(response http.ResponseWriter, _ *http.Request) {
				writeSteamResponse(response, http.StatusOK, `{"success":true,"start":0,"pagesize":1,"total_count":1,"results":[`+testCase.result+`]}`)
			})
			_, err := client.FetchCandidatePage(context.Background(), 0, 1)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestFetchSteamCandidatePageValidatesPaginationArguments(t *testing.T) {
	client := NewSteamClient("https://example.invalid", &http.Client{})
	for _, testCase := range []struct{ start, count int }{{-1, 10}, {0, 0}, {0, 101}} {
		if _, err := client.FetchCandidatePage(context.Background(), testCase.start, testCase.count); err == nil {
			t.Fatalf("start=%d count=%d was accepted", testCase.start, testCase.count)
		}
	}
}

func testSteamClient(t *testing.T, handler http.HandlerFunc) *SteamClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewSteamClient(server.URL, server.Client())
}

func writeSteamResponse(response http.ResponseWriter, status int, body string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = fmt.Fprint(response, body)
}
