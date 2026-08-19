package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

func TestRunScanCycleComplete(t *testing.T) {
	client, requests := scanTestClient(t, func(path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, `{"items":[{"id":1,"cnName":"candidate one"},{"id":2,"cnName":"candidate two"}]}`
		case path == "/api/commodity/search" && page == 2:
			return http.StatusOK, `{"items":[]}`
		case path == "/api/commodity/exchange" && page == 1:
			return http.StatusOK, "[" + marketJSON(1, "0.6000", 350, "10.00", "2026-08-18 16:30:00") + "," +
				marketJSON(99, "0.5900", 400, "20.00", "2026-08-18 16:31:00") + "]"
		case path == "/api/commodity/exchange" && page == 2:
			return http.StatusOK, "[]"
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	stateStore := openTestStore(t)
	processingStarted := time.Now().UTC()

	result, err := RunScanCycle(context.Background(), client, stateStore, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := ScanResult{CandidateCount: 2, MatchedCount: 1, ProcessedCount: 1}
	if result != want {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
	if got := *requests; !equalStrings(got, []string{
		"/api/commodity/search:1", "/api/commodity/search:2",
		"/api/commodity/exchange:1", "/api/commodity/exchange:2",
	}) {
		t.Fatalf("requests = %#v", got)
	}
	state, found, err := stateStore.GetItemState(context.Background(), 1)
	if err != nil || !found {
		t.Fatalf("stored matched item: found=%t err=%v", found, err)
	}
	if state.Ratio != 6000 || state.SteamDailyVolume != 350 || state.Tier != core.TierA {
		t.Fatalf("stored matched state = %#v", state)
	}
	if state.NextCheckAt.IsZero() || !state.NextCheckAt.After(processingStarted) {
		t.Fatalf("scan did not initialize a future NextCheckAt: %s", state.NextCheckAt)
	}
	if _, found, err := stateStore.GetItemState(context.Background(), 99); err != nil || found {
		t.Fatalf("unmatched exchange item was stored: found=%t err=%v", found, err)
	}
}

func TestRunScanCycleCandidateUsesRawEmptyPage(t *testing.T) {
	client, requests := scanTestClient(t, func(path string, page int) (int, string) {
		if path == "/api/commodity/search" {
			switch page {
			case 1:
				return http.StatusOK, `{"items":[{"id":1}]}`
			case 2:
				return http.StatusOK, `{"items":[{"id":null}]}`
			case 3:
				return http.StatusOK, `{"items":[{"id":2}]}`
			case 4:
				return http.StatusOK, `{"items":[]}`
			}
		}
		if path == "/api/commodity/exchange" && page == 1 {
			return http.StatusOK, "[]"
		}
		return http.StatusInternalServerError, "{}"
	})
	stateStore := openTestStore(t)

	result, err := RunScanCycle(context.Background(), client, stateStore, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.CandidateCount != 2 || result.MatchedCount != 0 || result.ProcessedCount != 0 {
		t.Fatalf("result = %#v", result)
	}
	if got := *requests; !equalStrings(got, []string{
		"/api/commodity/search:1", "/api/commodity/search:2",
		"/api/commodity/search:3", "/api/commodity/search:4",
		"/api/commodity/exchange:1",
	}) {
		t.Fatalf("requests = %#v", got)
	}
}

func TestRunScanCycleKeepsNewestDuplicate(t *testing.T) {
	client, _ := scanTestClient(t, func(path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, `{"items":[{"id":53}]}`
		case path == "/api/commodity/search" && page == 2:
			return http.StatusOK, `{"items":[]}`
		case path == "/api/commodity/exchange" && page == 1:
			return http.StatusOK, "[" + marketJSON(53, "0.6200", 300, "20.00", "2026-08-18 16:30:00") + "]"
		case path == "/api/commodity/exchange" && page == 2:
			return http.StatusOK, "[" + marketJSON(53, "0.5900", 400, "17.73", "2026-08-18 16:40:00") + "]"
		case path == "/api/commodity/exchange" && page == 3:
			return http.StatusOK, "[]"
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	stateStore := openTestStore(t)

	result, err := RunScanCycle(context.Background(), client, stateStore, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result != (ScanResult{CandidateCount: 1, MatchedCount: 1, ProcessedCount: 1, DuplicateCount: 1}) {
		t.Fatalf("result = %#v", result)
	}
	state, found, err := stateStore.GetItemState(context.Background(), 53)
	if err != nil || !found {
		t.Fatalf("get state: found=%t err=%v", found, err)
	}
	if state.Ratio != 5900 || state.SteamPrice != "17.73" || state.Tier != core.TierA {
		t.Fatalf("newer duplicate was not saved: %#v", state)
	}
}

func TestRunScanCycleValidDuplicateTimeBeatsInvalid(t *testing.T) {
	client, _ := scanTestClient(t, func(path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, `{"items":[{"id":53}]}`
		case path == "/api/commodity/search" && page == 2:
			return http.StatusOK, `{"items":[]}`
		case path == "/api/commodity/exchange" && page == 1:
			return http.StatusOK, "[" + marketJSON(53, "0.5900", 400, "17.73", "invalid") + "]"
		case path == "/api/commodity/exchange" && page == 2:
			return http.StatusOK, "[" + marketJSON(53, "0.6200", 300, "20.00", "2026-08-18 16:40:00") + "]"
		case path == "/api/commodity/exchange" && page == 3:
			return http.StatusOK, "[]"
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	stateStore := openTestStore(t)

	result, err := RunScanCycle(context.Background(), client, stateStore, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.DuplicateCount != 1 || result.ProcessedCount != 1 {
		t.Fatalf("result = %#v", result)
	}
	state, found, err := stateStore.GetItemState(context.Background(), 53)
	if err != nil || !found {
		t.Fatalf("get state: found=%t err=%v", found, err)
	}
	if state.Ratio != 6200 || state.SteamPrice != "20.00" || state.Tier != core.TierB1 {
		t.Fatalf("valid timestamp did not win: %#v", state)
	}
}

func TestRunScanCycleAbsenceDoesNotChangeState(t *testing.T) {
	client, _ := scanTestClient(t, func(path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, `{"items":[{"id":53}]}`
		case path == "/api/commodity/search" && page == 2:
			return http.StatusOK, `{"items":[]}`
		case path == "/api/commodity/exchange" && page == 1:
			return http.StatusOK, "[]"
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	stateStore := openTestStore(t)
	original := store.ItemState{
		ItemID: 53, MarketHashName: "old hash", NameCN: "old name",
		Ratio: 5900, SteamDailyVolume: 400, SteamPrice: "17.73", Tier: core.TierA,
		NextCheckAt: testCheckedAt.Add(time.Hour),
	}
	saveState(t, stateStore, original)

	result, err := RunScanCycle(context.Background(), client, stateStore, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result != (ScanResult{CandidateCount: 1}) {
		t.Fatalf("result = %#v", result)
	}
	got, found, err := stateStore.GetItemState(context.Background(), 53)
	if err != nil || !found || got != original {
		t.Fatalf("absent item changed: got=%#v found=%t err=%v", got, found, err)
	}
}

func TestRunScanCycleCandidateFailureStopsBeforeExchange(t *testing.T) {
	exchangeRequests := 0
	client, _ := scanTestClient(t, func(path string, page int) (int, string) {
		if path == "/api/commodity/exchange" {
			exchangeRequests++
		}
		if path == "/api/commodity/search" && page == 1 {
			return http.StatusInternalServerError, "{}"
		}
		return http.StatusOK, "[]"
	})
	stateStore := openTestStore(t)

	result, err := RunScanCycle(context.Background(), client, stateStore, 0)
	if err == nil {
		t.Fatal("candidate failure returned nil error")
	}
	if result != (ScanResult{}) || exchangeRequests != 0 {
		t.Fatalf("result=%#v exchange requests=%d", result, exchangeRequests)
	}
	if _, found, getErr := stateStore.GetItemState(context.Background(), 1); getErr != nil || found {
		t.Fatalf("candidate failure changed state: found=%t err=%v", found, getErr)
	}
}

func TestRunScanCycleProcessesExchangePartialResult(t *testing.T) {
	client, _ := scanTestClient(t, func(path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, `{"items":[{"id":1},{"id":2}]}`
		case path == "/api/commodity/search" && page == 2:
			return http.StatusOK, `{"items":[]}`
		case path == "/api/commodity/exchange" && page == 1:
			return http.StatusOK, "[" + marketJSON(1, "0.6000", 350, "10.00", "2026-08-18 16:30:00") + "]"
		case path == "/api/commodity/exchange" && page == 2:
			return http.StatusInternalServerError, "{}"
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	stateStore := openTestStore(t)
	absent := store.ItemState{
		ItemID: 2, MarketHashName: "old hash", NameCN: "old name",
		Ratio: 5900, SteamDailyVolume: 400, SteamPrice: "20.00", Tier: core.TierA,
		NextCheckAt: testCheckedAt.Add(time.Hour),
	}
	saveState(t, stateStore, absent)

	result, err := RunScanCycle(context.Background(), client, stateStore, 0)
	if err == nil {
		t.Fatal("partial exchange scan returned nil error")
	}
	if result != (ScanResult{CandidateCount: 2, MatchedCount: 1, ProcessedCount: 1}) {
		t.Fatalf("result = %#v", result)
	}
	if _, found, getErr := stateStore.GetItemState(context.Background(), 1); getErr != nil || !found {
		t.Fatalf("partial matched item not saved: found=%t err=%v", found, getErr)
	}
	got, found, getErr := stateStore.GetItemState(context.Background(), 2)
	if getErr != nil || !found || got != absent {
		t.Fatalf("partial absence changed state: got=%#v found=%t err=%v", got, found, getErr)
	}
}

func TestRunScanCycleStopsOnProcessFailure(t *testing.T) {
	client, _ := scanTestClient(t, func(path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, `{"items":[{"id":1}]}`
		case path == "/api/commodity/search" && page == 2:
			return http.StatusOK, `{"items":[]}`
		case path == "/api/commodity/exchange" && page == 1:
			return http.StatusOK, "[" + marketJSON(1, "0.6000", 350, "10.00", "2026-08-18 16:30:00") + "]"
		case path == "/api/commodity/exchange" && page == 2:
			return http.StatusOK, "[]"
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	stateStore, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := RunScanCycle(context.Background(), client, stateStore, 0)
	if err == nil {
		t.Fatal("closed store returned nil error")
	}
	if result != (ScanResult{CandidateCount: 1, MatchedCount: 1}) {
		t.Fatalf("result = %#v", result)
	}
}

type scanResponder func(path string, page int) (status int, data string)

func scanTestClient(t *testing.T, respond scanResponder) (*collector.SMISClient, *[]string) {
	t.Helper()
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		pageNumber, err := strconv.Atoi(body["pageNum"].(json.Number).String())
		if err != nil {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		requests = append(requests, request.URL.Path+":"+strconv.Itoa(pageNumber))
		status, data := respond(request.URL.Path, pageNumber)
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(status)
		if status >= 200 && status < 300 {
			_, _ = fmt.Fprintf(response, `{"code":200,"message":"success","data":%s}`, data)
			return
		}
		_, _ = fmt.Fprint(response, data)
	}))
	t.Cleanup(server.Close)
	return collector.NewSMISClient(server.URL, server.Client()), &requests
}

func marketJSON(itemID int64, ratio string, volume int, price, updatedAt string) string {
	return fmt.Sprintf(
		`{"id":%d,"hashName":"hash-%d","cnName":"name-%d","extremeRatio":%s,"steamTransactionQuantity":%d,"steamSellPrice":%s,"updateTime":%q}`,
		itemID, itemID, itemID, ratio, volume, price, updatedAt,
	)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
