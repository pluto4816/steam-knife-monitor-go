package app

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
)

func TestRunTargetedScanCycleUsesOnlyCatalogDetails(t *testing.T) {
	client, requests := runtimeTestClient(t, func(method, path string, _ int) (int, string) {
		if method != http.MethodGet {
			return http.StatusInternalServerError, `{}`
		}
		switch path {
		case "/api/commodity/1":
			return http.StatusOK, detailData(1, "0.59", 400, "2026-08-24 01:00:00")
		case "/api/commodity/2":
			return http.StatusOK, detailData(2, "0.70", 300, "2026-08-24 01:00:01")
		default:
			return http.StatusInternalServerError, `{}`
		}
	})
	candidates := []catalog.Item{
		{ItemID: 2, MarketHashName: "hash-2", NameCN: "二"},
		{ItemID: 1, MarketHashName: "hash-1", NameCN: "一"},
	}
	stateStore := openTestStore(t)

	result, err := RunTargetedScanCycle(context.Background(), client, stateStore, candidates, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result != (ScanResult{CandidateCount: 2, MatchedCount: 2, ProcessedCount: 2}) {
		t.Fatalf("result = %#v", result)
	}
	got := requests.snapshot()
	if len(got) != 2 || got[0].Path != "/api/commodity/1" || got[1].Path != "/api/commodity/2" {
		t.Fatalf("requests = %#v, want sorted detail requests only", got)
	}
	if requests.countPath("/api/commodity/search") != 0 || requests.countPath("/api/commodity/exchange") != 0 {
		t.Fatalf("unexpected broad requests = %#v", got)
	}
}

func TestRunTargetedScanCycleStopsOnRestriction(t *testing.T) {
	client, requests := runtimeTestClient(t, func(_ string, _ string, _ int) (int, string) {
		return http.StatusForbidden, `{}`
	})
	candidates := []catalog.Item{
		{ItemID: 1, MarketHashName: "hash-1", NameCN: "一"},
		{ItemID: 2, MarketHashName: "hash-2", NameCN: "二"},
	}
	result, err := RunTargetedScanCycle(context.Background(), client, openTestStore(t), candidates, 0)
	if !errors.Is(err, collector.ErrAccessRestricted) {
		t.Fatalf("error = %v, want ErrAccessRestricted", err)
	}
	if result != (ScanResult{CandidateCount: 2}) || requests.total() != 1 {
		t.Fatalf("result=%#v requests=%#v", result, requests.snapshot())
	}
}

func TestRunTargetedScanCycleRejectsHashMismatchWithoutStateChange(t *testing.T) {
	client, _ := runtimeTestClient(t, func(_ string, _ string, _ int) (int, string) {
		return http.StatusOK, detailData(1, "0.59", 400, "2026-08-24 01:00:00")
	})
	stateStore := openTestStore(t)
	result, err := RunTargetedScanCycle(
		context.Background(),
		client,
		stateStore,
		[]catalog.Item{{ItemID: 1, MarketHashName: "different", NameCN: "一"}},
		0,
	)
	if err == nil {
		t.Fatal("hash mismatch returned nil error")
	}
	if result != (ScanResult{CandidateCount: 1}) {
		t.Fatalf("result = %#v", result)
	}
	if _, found, getErr := stateStore.GetItemState(context.Background(), 1); getErr != nil || found {
		t.Fatalf("state changed: found=%t err=%v", found, getErr)
	}
}

func TestRunTargetedScanCycleResumesAtFirstUnfinishedCandidate(t *testing.T) {
	candidates := []catalog.Item{
		{ItemID: 1, MarketHashName: "hash-1", NameCN: "一"},
		{ItemID: 2, MarketHashName: "hash-2", NameCN: "二"},
		{ItemID: 3, MarketHashName: "hash-3", NameCN: "三"},
	}
	stateStore := openTestStore(t)
	firstClient, firstRequests := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		switch path {
		case "/api/commodity/1":
			return http.StatusOK, detailData(1, "0.70", 300, "2026-08-24 01:00:00")
		case "/api/commodity/2":
			return http.StatusForbidden, `{}`
		default:
			return http.StatusInternalServerError, `{}`
		}
	})
	first, err := RunTargetedScanCycle(context.Background(), firstClient, stateStore, candidates, 0)
	if !errors.Is(err, collector.ErrAccessRestricted) {
		t.Fatalf("first error = %v, want ErrAccessRestricted", err)
	}
	if first != (ScanResult{CandidateCount: 3, MatchedCount: 1, ProcessedCount: 1}) {
		t.Fatalf("first result = %#v", first)
	}
	if got := firstRequests.snapshot(); len(got) != 2 || got[0].Path != "/api/commodity/1" || got[1].Path != "/api/commodity/2" {
		t.Fatalf("first requests = %#v", got)
	}

	checkpoint, found, err := stateStore.GetTargetedScanCheckpoint(context.Background())
	if err != nil || !found || checkpoint.CompletedCount != 1 || checkpoint.LastItemID != 1 {
		t.Fatalf("checkpoint=%#v found=%t err=%v", checkpoint, found, err)
	}
	secondClient, secondRequests := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		switch path {
		case "/api/commodity/2":
			return http.StatusOK, detailData(2, "0.65", 300, "2026-08-24 01:00:01")
		case "/api/commodity/3":
			return http.StatusOK, detailData(3, "0.60", 350, "2026-08-24 01:00:02")
		default:
			return http.StatusInternalServerError, `{}`
		}
	})
	second, err := RunTargetedScanCycle(context.Background(), secondClient, stateStore, candidates, 0)
	if err != nil {
		t.Fatal(err)
	}
	if second != (ScanResult{CandidateCount: 3, ResumedCount: 1, MatchedCount: 2, ProcessedCount: 2}) {
		t.Fatalf("second result = %#v", second)
	}
	if got := secondRequests.snapshot(); len(got) != 2 || got[0].Path != "/api/commodity/2" || got[1].Path != "/api/commodity/3" {
		t.Fatalf("second requests = %#v", got)
	}
	if _, found, err := stateStore.GetTargetedScanCheckpoint(context.Background()); err != nil || found {
		t.Fatalf("completed checkpoint found=%t err=%v", found, err)
	}
}

func TestRunTargetedScanCycleCatalogChangeInvalidatesCheckpoint(t *testing.T) {
	stateStore := openTestStore(t)
	original := []catalog.Item{
		{ItemID: 1, MarketHashName: "hash-1", NameCN: "一"},
		{ItemID: 2, MarketHashName: "hash-2", NameCN: "二"},
	}
	firstClient, _ := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		if path == "/api/commodity/1" {
			return http.StatusOK, detailData(1, "0.70", 300, "2026-08-24 01:00:00")
		}
		return http.StatusForbidden, `{}`
	})
	if _, err := RunTargetedScanCycle(context.Background(), firstClient, stateStore, original, 0); !errors.Is(err, collector.ErrAccessRestricted) {
		t.Fatalf("first error = %v, want ErrAccessRestricted", err)
	}

	changed := append(append([]catalog.Item(nil), original...), catalog.Item{
		ItemID: 3, MarketHashName: "hash-3", NameCN: "三",
	})
	secondClient, secondRequests := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		if path == "/api/commodity/1" {
			return http.StatusOK, detailData(1, "0.69", 300, "2026-08-24 01:01:00")
		}
		return http.StatusForbidden, `{}`
	})
	result, err := RunTargetedScanCycle(context.Background(), secondClient, stateStore, changed, 0)
	if !errors.Is(err, collector.ErrAccessRestricted) {
		t.Fatalf("second error = %v, want ErrAccessRestricted", err)
	}
	if result.ResumedCount != 0 || result.ProcessedCount != 1 {
		t.Fatalf("result = %#v, want restart from beginning", result)
	}
	if got := secondRequests.snapshot(); len(got) != 2 || got[0].Path != "/api/commodity/1" {
		t.Fatalf("second requests = %#v, want first item requested again", got)
	}
}
