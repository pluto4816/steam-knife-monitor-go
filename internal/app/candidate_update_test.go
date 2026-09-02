package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
)

func TestRunCandidateUpdateReusesMappingsAndWritesCompleteCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidates.json")
	previous := catalog.Catalog{
		Version:     catalog.Version,
		GeneratedAt: time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC),
		Criteria:    catalog.RequiredCriteria,
		Items: []catalog.Item{
			{ItemID: 1, MarketHashName: "AK-47 | One (Field-Tested)", NameCN: "旧一"},
			{ItemID: 2, MarketHashName: "AK-47 | Stale (Field-Tested)", NameCN: "旧二"},
		},
	}
	if err := catalog.SaveAtomic(path, previous); err != nil {
		t.Fatal(err)
	}

	definitionsClient, snapshotSHA256 := candidateUpdateDefinitionsClient(t, []string{
		"AK-47 | One (Field-Tested)",
		"AWP | Definition Only (Field-Tested)",
		"P250 | Two (Field-Tested)",
	})
	smisClient, requests := runtimeTestClient(t, func(_ string, path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, `{"items":[{"id":1,"cnName":"新一"},{"id":2,"cnName":"新二"},{"id":3,"cnName":"三"}],"pageNum":1,"pageSize":50}`
		case path == "/api/commodity/search" && page == 2:
			return http.StatusOK, `{"items":[],"pageNum":2,"pageSize":50}`
		case path == "/api/commodity/2":
			return http.StatusOK, identityJSON(2, "P250 | Two (Field-Tested)", "新二")
		case path == "/api/commodity/3":
			return http.StatusOK, identityJSON(3, "MP7 | Three (Field-Tested)", "三")
		default:
			return http.StatusInternalServerError, `{}`
		}
	})

	result, err := RunCandidateUpdate(context.Background(), definitionsClient, smisClient, path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := (CandidateUpdateResult{
		DefinitionRecordCount: 3, DefinitionCandidateCount: 3,
		DefinitionSnapshotSHA256: snapshotSHA256,
		SMISCandidateCount:       3, MatchedCount: 2, ReusedCount: 1,
		DetailFetchedCount: 2, DefinitionOnlyCount: 1,
		DefinitionOnlyNames: []string{"AWP | Definition Only (Field-Tested)"}, SMISOnlyCount: 1,
	})
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
	if requests.countPath("/api/commodity/1") != 0 || requests.countPath("/api/commodity/2") != 1 || requests.countPath("/api/commodity/3") != 1 {
		t.Fatalf("detail requests = %#v", requests.snapshot())
	}
	updated, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Items) != 2 || updated.Items[0].ItemID != 1 || updated.Items[0].NameCN != "新一" || updated.Items[1].ItemID != 2 {
		t.Fatalf("updated catalog = %#v", updated)
	}
}

func TestRunCandidateUpdateFailurePreservesPreviousCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidates.json")
	previous := catalog.Catalog{
		Version:     catalog.Version,
		GeneratedAt: time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC),
		Criteria:    catalog.RequiredCriteria,
		Items: []catalog.Item{
			{ItemID: 1, MarketHashName: "AK-47 | Old (Field-Tested)", NameCN: "旧"},
		},
	}
	if err := catalog.SaveAtomic(path, previous); err != nil {
		t.Fatal(err)
	}
	definitionsClient, _ := candidateUpdateDefinitionsClient(t, []string{"P250 | New (Field-Tested)"})
	smisClient, _ := runtimeTestClient(t, func(_ string, path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, `{"items":[{"id":2,"cnName":"新"}],"pageNum":1,"pageSize":50}`
		case path == "/api/commodity/search" && page == 2:
			return http.StatusOK, `{"items":[],"pageNum":2,"pageSize":50}`
		default:
			return http.StatusInternalServerError, `{}`
		}
	})
	if _, err := RunCandidateUpdate(context.Background(), definitionsClient, smisClient, path, 0, 0); err == nil {
		t.Fatal("failed identity update returned nil error")
	}
	got, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.GeneratedAt.Equal(previous.GeneratedAt) || len(got.Items) != 1 || got.Items[0] != previous.Items[0] {
		t.Fatalf("previous catalog changed: %#v", got)
	}
}

func TestFetchAllSMISCandidatesRejectsPartiallyParsedPage(t *testing.T) {
	client, requests := runtimeTestClient(t, func(_ string, path string, page int) (int, string) {
		if path == "/api/commodity/search" && page == 1 {
			return http.StatusOK, `{"items":[{"id":1,"cnName":"一"},{"id":0,"cnName":"坏数据"}],"pageNum":1,"pageSize":50}`
		}
		return http.StatusInternalServerError, `{}`
	})

	_, err := fetchAllSMISCandidates(context.Background(), client, 0)
	if err == nil || !strings.Contains(err.Error(), "parsed 1 of 2 items") {
		t.Fatalf("error = %v, want partially parsed page failure", err)
	}
	if requests.countPath("/api/commodity/search") != 1 {
		t.Fatalf("requests = %#v, want stop after malformed page", requests.snapshot())
	}
}

func TestFetchAllSMISCandidatesStopsOnNoProgress(t *testing.T) {
	client, requests := runtimeTestClient(t, func(_ string, path string, page int) (int, string) {
		if path == "/api/commodity/search" && (page == 1 || page == 2) {
			return http.StatusOK, fmt.Sprintf(`{"items":[{"id":1,"cnName":"一"}],"pageNum":%d,"pageSize":50}`, page)
		}
		return http.StatusInternalServerError, `{}`
	})

	_, err := fetchAllSMISCandidates(context.Background(), client, 0)
	if err == nil || !strings.Contains(err.Error(), "page 2 made no progress") {
		t.Fatalf("error = %v, want no-progress failure", err)
	}
	if requests.countPath("/api/commodity/search") != 2 {
		t.Fatalf("requests = %#v, want stop after repeated page", requests.snapshot())
	}
}

func TestFetchAllSMISCandidatesRejectsUnexpectedPagination(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{"wrong page number", smisCandidatePageJSON(2, collector.SMISPageSize, 1), "pageNum 2 does not match request 1"},
		{"wrong page size", smisCandidatePageJSON(1, 25, 1), "pageSize 25, want 50"},
		{"oversized page", smisCandidatePageJSON(1, collector.SMISPageSize, 51), "contains 51 items, exceeds pageSize 50"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client, requests := runtimeTestClient(t, func(_ string, path string, page int) (int, string) {
				if path == "/api/commodity/search" && page == 1 {
					return http.StatusOK, testCase.data
				}
				return http.StatusInternalServerError, `{}`
			})
			_, err := fetchAllSMISCandidates(context.Background(), client, 0)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
			if requests.countPath("/api/commodity/search") != 1 {
				t.Fatalf("requests = %#v, want stop after invalid page", requests.snapshot())
			}
		})
	}
}

func candidateUpdateDefinitionsClient(t *testing.T, names []string) (*collector.CS2DefinitionsClient, string) {
	t.Helper()
	var body strings.Builder
	body.WriteByte('[')
	for index, name := range names {
		if index > 0 {
			body.WriteByte(',')
		}
		_, _ = fmt.Fprintf(
			&body,
			`{"market_hash_name":%q,"rarity":{"id":"rarity_rare_weapon","name":"Mil-Spec Grade"},"wear":{"id":"SFUI_InvTooltip_Wear_Amount_2","name":"Field-Tested"},"stattrak":false,"souvenir":false}`,
			name,
		)
	}
	body.WriteByte(']')
	responseBody := body.String()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("unexpected definitions method %s", request.Method)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(response, responseBody)
	}))
	t.Cleanup(server.Close)
	digest := sha256.Sum256([]byte(responseBody))
	return collector.NewCS2DefinitionsClient(server.URL, server.Client()), hex.EncodeToString(digest[:])
}

func identityJSON(itemID int64, hashName, nameCN string) string {
	return fmt.Sprintf(
		`{"id":%d,"hashName":%q,"cnName":%q,"rarity":"军规级","quality":"普通","exterior":"久经沙场"}`,
		itemID, hashName, nameCN,
	)
}

func smisCandidatePageJSON(pageNum, pageSize, itemCount int) string {
	var builder strings.Builder
	builder.WriteString(`{"items":[`)
	for index := 1; index <= itemCount; index++ {
		if index > 1 {
			builder.WriteByte(',')
		}
		_, _ = fmt.Fprintf(&builder, `{"id":%d,"cnName":"item-%d"}`, index, index)
	}
	_, _ = fmt.Fprintf(&builder, `],"pageNum":%d,"pageSize":%d}`, pageNum, pageSize)
	return builder.String()
}
