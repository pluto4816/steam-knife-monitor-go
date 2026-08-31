package app

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

func TestRunOneShotScansProcessesDueItemsSendsAndReturns(t *testing.T) {
	stateStore := openTestStore(t)
	previous := dueState(53, time.Now().UTC().Add(-time.Minute))
	previous.Ratio = 6250
	previous.SteamDailyVolume = 300
	previous.Tier = core.TierB1
	saveState(t, stateStore, previous)
	client, requests := runtimeTestClient(t, func(method, path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, `{"items":[]}`
		case method == http.MethodGet && path == "/api/commodity/53":
			return http.StatusOK, detailData(53, "0.59", 400, "2026-08-19 16:06:44")
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	sender := &recordingSender{}

	result, err := RunOneShot(context.Background(), client, stateStore, runtimeTestConfig(), sender)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scan != (ScanResult{}) {
		t.Fatalf("scan result = %#v", result.Scan)
	}
	if result.DueChecks != (DueCheckResult{DueCount: 1, ProcessedCount: 1}) {
		t.Fatalf("due result = %#v", result.DueChecks)
	}
	if result.TierCounts != (store.ItemTierCounts{A: 1}) {
		t.Fatalf("tier counts = %#v", result.TierCounts)
	}
	if result.Notifications != (SendResult{PendingCount: 1, SentCount: 1}) {
		t.Fatalf("notification result = %#v", result.Notifications)
	}
	if len(sender.alerts) != 1 {
		t.Fatalf("sent alerts = %d, want 1", len(sender.alerts))
	}
	if requests.total() != 2 || requests.countPath("/api/commodity/search") != 1 || requests.countPath("/api/commodity/53") != 1 {
		t.Fatalf("requests = %#v, want one full-scan request and one due request", requests.snapshot())
	}
}

func TestRunOneShotWithoutSenderLeavesPreviewAlertsPending(t *testing.T) {
	client, requests := runtimeTestClient(t, func(_ string, path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, `{"items":[{"id":1}]}`
		case path == "/api/commodity/search" && page == 2:
			return http.StatusOK, `{"items":[]}`
		case path == "/api/commodity/exchange" && page == 1:
			return http.StatusOK, "[" + marketJSON(1, "0.5900", 400, "10.00", "2026-08-19 16:06:44") + "]"
		case path == "/api/commodity/exchange" && page == 2:
			return http.StatusOK, "[]"
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	stateStore := openTestStore(t)

	result, err := RunOneShot(context.Background(), client, stateStore, runtimeTestConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scan != (ScanResult{CandidateCount: 1, MatchedCount: 1, ProcessedCount: 1}) {
		t.Fatalf("scan result = %#v", result.Scan)
	}
	if result.TierCounts != (store.ItemTierCounts{A: 1}) {
		t.Fatalf("tier counts = %#v", result.TierCounts)
	}
	if result.Notifications != (SendResult{PendingCount: 1}) {
		t.Fatalf("notification result = %#v", result.Notifications)
	}
	alerts := pendingAlerts(t, stateStore)
	if len(alerts) != 1 || alerts[0].ItemID != 1 {
		t.Fatalf("preview alerts = %#v, want one pending alert", alerts)
	}
	if requests.total() != 4 {
		t.Fatalf("requests = %#v, want one bounded full scan", requests.snapshot())
	}
}

func TestRunOneShotStopsOnAccessRestriction(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, dueState(53, time.Now().UTC().Add(-time.Minute)))
	client, requests := runtimeTestClient(t, func(_ string, _ string, _ int) (int, string) {
		return http.StatusForbidden, "{}"
	})

	_, err := RunOneShot(context.Background(), client, stateStore, runtimeTestConfig(), &recordingSender{})
	if !errors.Is(err, collector.ErrAccessRestricted) {
		t.Fatalf("error = %v, want ErrAccessRestricted", err)
	}
	if requests.total() != 1 || requests.countPath("/api/commodity/53") != 0 {
		t.Fatalf("requests after restriction = %#v", requests.snapshot())
	}
}

func TestRunOneShotIncompleteTargetedScanDoesNotSend(t *testing.T) {
	stateStore := openTestStore(t)
	client, requests := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		switch path {
		case "/api/commodity/1":
			return http.StatusOK, detailData(1, "0.59", 400, "2026-08-24 01:00:00")
		case "/api/commodity/2":
			return http.StatusForbidden, `{}`
		default:
			return http.StatusInternalServerError, `{}`
		}
	})
	config := runtimeTestConfig()
	config.Candidates = []catalog.Item{
		{ItemID: 1, MarketHashName: "hash-1", NameCN: "一"},
		{ItemID: 2, MarketHashName: "hash-2", NameCN: "二"},
	}
	sender := &recordingSender{}

	result, err := RunOneShot(context.Background(), client, stateStore, config, sender)
	if !errors.Is(err, collector.ErrAccessRestricted) {
		t.Fatalf("error = %v, want ErrAccessRestricted", err)
	}
	if result.Scan != (ScanResult{CandidateCount: 2, MatchedCount: 1, ProcessedCount: 1}) {
		t.Fatalf("scan result = %#v", result.Scan)
	}
	if len(sender.alerts) != 0 {
		t.Fatalf("sent alerts = %#v, want none after incomplete scan", sender.alerts)
	}
	alerts := pendingAlerts(t, stateStore)
	if len(alerts) != 1 || alerts[0].ItemID != 1 {
		t.Fatalf("pending alerts = %#v, want preserved item 1 alert", alerts)
	}
	if requests.total() != 2 {
		t.Fatalf("requests = %#v, want stop after second detail", requests.snapshot())
	}
}

func TestRunOneShotRejectsInvalidConfigBeforeRequests(t *testing.T) {
	config := runtimeTestConfig()
	config.PageDelay = -time.Second
	if _, err := RunOneShot(context.Background(), nil, nil, config, nil); err == nil {
		t.Fatal("invalid config was accepted")
	}
}

func TestRunOneShotCountsOnlyConfiguredCandidates(t *testing.T) {
	stateStore := openTestStore(t)
	stale := dueState(99, time.Now().UTC().Add(-time.Hour))
	stale.Tier = core.TierA
	saveState(t, stateStore, stale)

	client, requests := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		switch path {
		case "/api/commodity/1":
			return http.StatusOK, detailData(1, "0.70", 300, "2026-08-24 01:00:00")
		case "/api/commodity/2":
			return http.StatusOK, detailData(2, "0.80", 100, "2026-08-24 01:00:01")
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	config := runtimeTestConfig()
	config.Candidates = []catalog.Item{
		{ItemID: 1, MarketHashName: "hash-1", NameCN: "一"},
		{ItemID: 2, MarketHashName: "hash-2", NameCN: "二"},
	}

	result, err := RunOneShot(context.Background(), client, stateStore, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.TierCounts != (store.ItemTierCounts{B3: 1, C: 1}) {
		t.Fatalf("tier counts = %#v, want only configured candidates", result.TierCounts)
	}
	if result.DueChecks != (DueCheckResult{}) {
		t.Fatalf("due checks = %#v, want stale item excluded", result.DueChecks)
	}
	if requests.total() != 2 || requests.countPath("/api/commodity/99") != 0 {
		t.Fatalf("requests = %#v, want configured details only", requests.snapshot())
	}
	got, found, getErr := stateStore.GetItemState(context.Background(), 99)
	if getErr != nil || !found || got != stale {
		t.Fatalf("stale item changed: got=%#v found=%t err=%v", got, found, getErr)
	}
}
