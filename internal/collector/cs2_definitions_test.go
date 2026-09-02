package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchCS2DefinitionSnapshotRequestFilterAndHash(t *testing.T) {
	body := `[
		{"market_hash_name":"P250 | Two (Field-Tested)","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_2","name":"Field-Tested"},"stattrak":false,"souvenir":false},
		{"market_hash_name":"AK-47 | One (Field-Tested)","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_2","name":"Field-Tested"},"stattrak":false,"souvenir":false},
		{"market_hash_name":"AK-47 | Restricted (Field-Tested)","rarity":{"id":"rarity_mythical_weapon","name":"Restricted"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_2","name":"Field-Tested"},"stattrak":false,"souvenir":false},
		{"market_hash_name":"AK-47 | One (Minimal Wear)","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_1","name":"Minimal Wear"},"stattrak":false,"souvenir":false},
		{"market_hash_name":"StatTrak™ AK-47 | One (Field-Tested)","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_2","name":"Field-Tested"},"stattrak":true,"souvenir":false},
		{"market_hash_name":"Souvenir AK-47 | One (Field-Tested)","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_2","name":"Field-Tested"},"stattrak":false,"souvenir":true}
	]`
	var gotMethod, gotAccept, gotUserAgent string
	client := testCS2DefinitionsClient(t, func(response http.ResponseWriter, request *http.Request) {
		gotMethod = request.Method
		gotAccept = request.Header.Get("Accept")
		gotUserAgent = request.Header.Get("User-Agent")
		writeCS2DefinitionsResponse(response, http.StatusOK, body)
	})

	snapshot, err := client.FetchCandidateSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RecordCount != 6 || len(snapshot.Candidates) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Candidates[0].MarketHashName != "AK-47 | One (Field-Tested)" ||
		snapshot.Candidates[1].MarketHashName != "P250 | Two (Field-Tested)" {
		t.Fatalf("candidates = %#v", snapshot.Candidates)
	}
	digest := sha256.Sum256([]byte(body))
	if snapshot.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("SHA256 = %q", snapshot.SHA256)
	}
	if gotMethod != http.MethodGet || gotAccept != "application/json" || gotUserAgent != cs2DefinitionsUserAgent {
		t.Fatalf("request method=%q Accept=%q User-Agent=%q", gotMethod, gotAccept, gotUserAgent)
	}
}

func TestFetchCS2DefinitionSnapshotStopsOnRestrictionsAndEmptyBody(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{"unauthorized", http.StatusUnauthorized, "application/json", `{}`},
		{"forbidden", http.StatusForbidden, "application/json", `{}`},
		{"rate limited", http.StatusTooManyRequests, "application/json", `{}`},
		{"empty success", http.StatusOK, "application/json", "  \n"},
		{"challenge", http.StatusOK, "text/html", `<html><title>Just a moment...</title><body>cf-chl</body></html>`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client := testCS2DefinitionsClient(t, func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", testCase.contentType)
				response.WriteHeader(testCase.status)
				_, _ = fmt.Fprint(response, testCase.body)
			})
			_, err := client.FetchCandidateSnapshot(context.Background())
			if !errors.Is(err, ErrCS2DefinitionsAccessRestricted) {
				t.Fatalf("error = %v, want ErrCS2DefinitionsAccessRestricted", err)
			}
		})
	}
}

func TestFetchCS2DefinitionSnapshotRejectsInvalidData(t *testing.T) {
	valid := `{"market_hash_name":"AK-47 | One (Field-Tested)","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_2","name":"Field-Tested"},"stattrak":false,"souvenir":false}`
	tests := []struct {
		name string
		body string
		want string
	}{
		{"malformed JSON", `[`, "decode CS2 definitions"},
		{"multiple JSON values", `[` + valid + `] []`, "multiple JSON values"},
		{"no records", `[]`, "contains no records"},
		{"empty hash name", `[{"rarity":{"id":"other"}}]`, "empty market_hash_name"},
		{"wrong rarity name", `[{"market_hash_name":"x","rarity":{"id":"rarity_rare_weapon","name":"Restricted"},"wear":{"id":"other","name":"Factory New"},"stattrak":false,"souvenir":false}]`, "rarity name"},
		{"missing wear", `[{"market_hash_name":"x","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"stattrak":false,"souvenir":false}]`, "has no wear"},
		{"wrong wear ID", `[{"market_hash_name":"AK-47 | One (Field-Tested)","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"other","name":"Field-Tested"},"stattrak":false,"souvenir":false}]`, "wear ID"},
		{"missing quality flag", `[{"market_hash_name":"AK-47 | One (Field-Tested)","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_2","name":"Field-Tested"},"souvenir":false}]`, "missing quality flags"},
		{"wear conflicts with hash", `[{"market_hash_name":"AK-47 | One (Minimal Wear)","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_2","name":"Field-Tested"},"stattrak":false,"souvenir":false}]`, "conflicts with Field-Tested"},
		{"quality conflicts with hash", `[{"market_hash_name":"StatTrak™ AK-47 | One (Field-Tested)","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_2","name":"Field-Tested"},"stattrak":false,"souvenir":false}]`, "conflicts with normal quality"},
		{"duplicate candidate", `[` + valid + `,` + valid + `]`, "duplicate CS2 definition"},
		{"no candidates", `[{"market_hash_name":"AK-47 | One (Minimal Wear)","rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_1","name":"Minimal Wear"},"stattrak":false,"souvenir":false}]`, "filter returned no candidates"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client := testCS2DefinitionsClient(t, func(response http.ResponseWriter, _ *http.Request) {
				writeCS2DefinitionsResponse(response, http.StatusOK, testCase.body)
			})
			_, err := client.FetchCandidateSnapshot(context.Background())
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestFetchCS2DefinitionSnapshotRejectsOversizedResponse(t *testing.T) {
	client := testCS2DefinitionsClient(t, func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Length", fmt.Sprint(maxCS2DefinitionsResponseBytes+1))
		response.WriteHeader(http.StatusOK)
	})
	_, err := client.FetchCandidateSnapshot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %v, want response size failure", err)
	}
}

func testCS2DefinitionsClient(t *testing.T, handler http.HandlerFunc) *CS2DefinitionsClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewCS2DefinitionsClient(server.URL, server.Client())
}

func writeCS2DefinitionsResponse(response http.ResponseWriter, status int, body string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = fmt.Fprint(response, body)
}
