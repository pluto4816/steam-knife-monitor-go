package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

func TestRunDueChecksNoDueItems(t *testing.T) {
	stateStore := openTestStore(t)
	future := time.Now().UTC().Add(time.Hour)
	saveState(t, stateStore, dueState(1, future))

	requestCount := 0
	client, _ := dueTestClient(t, func(itemID int64) (int, string) {
		requestCount++
		return http.StatusInternalServerError, "{}"
	})
	result, err := RunDueChecks(context.Background(), client, stateStore, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result != (DueCheckResult{}) {
		t.Fatalf("result = %#v, want zero", result)
	}
	if requestCount != 0 {
		t.Fatalf("Detail requests = %d, want 0", requestCount)
	}
}

func TestRunDueChecksProcessesOneDueItem(t *testing.T) {
	stateStore := openTestStore(t)
	old := dueState(1, time.Now().UTC().Add(-time.Minute))
	old.Ratio = 6200
	old.SteamDailyVolume = 300
	old.Tier = core.TierB1
	saveState(t, stateStore, old)

	client, requests := dueTestClient(t, func(itemID int64) (int, string) {
		return http.StatusOK, detailData(itemID, "0.59", 400, "2026-08-19 16:06:44")
	})
	started := time.UnixMilli(time.Now().UTC().UnixMilli()).UTC()
	result, err := RunDueChecks(context.Background(), client, stateStore, 0)
	finished := time.UnixMilli(time.Now().UTC().UnixMilli()).UTC()
	if err != nil {
		t.Fatal(err)
	}
	if result != (DueCheckResult{DueCount: 1, ProcessedCount: 1}) {
		t.Fatalf("result = %#v", result)
	}
	if !equalInt64s(*requests, []int64{1}) {
		t.Fatalf("requests = %#v, want [1]", *requests)
	}
	got, found, err := stateStore.GetItemState(context.Background(), 1)
	if err != nil || !found {
		t.Fatalf("get processed state: found=%t err=%v", found, err)
	}
	if got.Ratio != 5900 || got.SteamDailyVolume != 400 || got.Tier != core.TierA {
		t.Fatalf("processed state = %#v", got)
	}
	if got.NextCheckAt.Before(started.Add(5*time.Minute)) ||
		got.NextCheckAt.After(finished.Add(5*time.Minute)) {
		t.Fatalf("NextCheckAt = %s, want checkedAt + 5m", got.NextCheckAt)
	}
}

func TestRunDueChecksProcessesDueItemsInStoreOrder(t *testing.T) {
	stateStore := openTestStore(t)
	now := time.Now().UTC()
	saveState(t, stateStore, dueState(2, now.Add(-2*time.Minute)))
	saveState(t, stateStore, dueState(3, now.Add(-time.Minute)))
	saveState(t, stateStore, dueState(1, now.Add(-time.Minute)))

	client, requests := dueTestClient(t, func(itemID int64) (int, string) {
		return http.StatusOK, detailData(itemID, "0.60", 350, "2026-08-19 16:06:44")
	})
	result, err := RunDueChecks(context.Background(), client, stateStore, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result != (DueCheckResult{DueCount: 3, ProcessedCount: 3}) {
		t.Fatalf("result = %#v", result)
	}
	if !equalInt64s(*requests, []int64{2, 1, 3}) {
		t.Fatalf("request order = %#v, want [2 1 3]", *requests)
	}
}

func TestRunDueChecksDetailFailurePreservesScheduleAndStops(t *testing.T) {
	stateStore := openTestStore(t)
	first := dueState(1, time.Now().UTC().Add(-2*time.Minute))
	second := dueState(2, time.Now().UTC().Add(-time.Minute))
	saveState(t, stateStore, first)
	saveState(t, stateStore, second)

	client, requests := dueTestClient(t, func(itemID int64) (int, string) {
		return http.StatusForbidden, "{}"
	})
	result, err := RunDueChecks(context.Background(), client, stateStore, 0)
	if !errors.Is(err, collector.ErrAccessRestricted) {
		t.Fatalf("error = %v, want ErrAccessRestricted", err)
	}
	if result != (DueCheckResult{DueCount: 2}) {
		t.Fatalf("result = %#v", result)
	}
	if !equalInt64s(*requests, []int64{1}) {
		t.Fatalf("requests = %#v, want only failed item 1", *requests)
	}
	for _, want := range []store.ItemState{first, second} {
		got, found, getErr := stateStore.GetItemState(context.Background(), want.ItemID)
		if getErr != nil || !found || got != want {
			t.Fatalf("item %d changed: got=%#v found=%t err=%v", want.ItemID, got, found, getErr)
		}
	}
}

func TestRunDueChecksProcessFailurePreservesOldState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process-failure.db")
	stateStore, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	old := dueState(1, time.Now().UTC().Add(-time.Minute))
	if err := stateStore.SaveItemState(context.Background(), old); err != nil {
		t.Fatal(err)
	}

	client, requests := dueTestClient(t, func(itemID int64) (int, string) {
		if err := stateStore.Close(); err != nil {
			t.Errorf("close Store in handler: %v", err)
		}
		return http.StatusOK, detailData(itemID, "0.59", 400, "2026-08-19 16:06:44")
	})
	result, err := RunDueChecks(context.Background(), client, stateStore, 0)
	if err == nil {
		t.Fatal("closed Store produced nil error")
	}
	if result != (DueCheckResult{DueCount: 1}) {
		t.Fatalf("result = %#v", result)
	}
	if !equalInt64s(*requests, []int64{1}) {
		t.Fatalf("requests = %#v, want [1]", *requests)
	}

	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, found, err := reopened.GetItemState(context.Background(), 1)
	if err != nil || !found || got != old {
		t.Fatalf("old state changed: got=%#v found=%t err=%v", got, found, err)
	}
}

func dueState(itemID int64, nextCheckAt time.Time) store.ItemState {
	return store.ItemState{
		ItemID:           itemID,
		MarketHashName:   fmt.Sprintf("old-hash-%d", itemID),
		NameCN:           fmt.Sprintf("old-name-%d", itemID),
		Ratio:            6000,
		SteamDailyVolume: 350,
		SteamPrice:       "1.15",
		Tier:             core.TierA,
		NextCheckAt:      time.UnixMilli(nextCheckAt.UTC().UnixMilli()).UTC(),
	}
}

type dueResponder func(itemID int64) (status int, data string)

func dueTestClient(t *testing.T, respond dueResponder) (*collector.SMISClient, *[]int64) {
	t.Helper()
	requests := []int64{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet ||
			!strings.HasPrefix(request.URL.Path, "/api/commodity/") {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		itemID, err := strconv.ParseInt(strings.TrimPrefix(request.URL.Path, "/api/commodity/"), 10, 64)
		if err != nil {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		requests = append(requests, itemID)
		status, data := respond(itemID)
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(status)
		if status >= 200 && status < 300 {
			_, _ = fmt.Fprintf(response, "{\"code\":200,\"message\":\"success\",\"data\":%s}", data)
			return
		}
		_, _ = fmt.Fprint(response, data)
	}))
	t.Cleanup(server.Close)
	return collector.NewSMISClient(server.URL, server.Client()), &requests
}

func detailData(itemID int64, minimumPrice string, volume int, updatedAt string) string {
	return fmt.Sprintf(
		"{\"id\":%d,\"hashName\":\"hash-%d\",\"cnName\":\"name-%d\","+
			"\"steamSellPrice\":1.15,\"steamTransactionQuantity\":%d,"+
			"\"buffSellPrice\":%s,\"uuypSellPrice\":0,\"c5SellPrice\":0,"+
			"\"igxeSellPrice\":0,\"ecoSellPrice\":0,\"updateTime\":%q}",
		itemID, itemID, itemID, volume, minimumPrice, updatedAt,
	)
}

func equalInt64s(left, right []int64) bool {
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
