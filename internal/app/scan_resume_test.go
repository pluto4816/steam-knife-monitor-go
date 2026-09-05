package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

func TestEvaluateTargetedScanResumeAcceptsExactRecentState(t *testing.T) {
	stateStore := openTestStore(t)
	candidates := resumeTestCandidates()
	now := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	saveResumeTestStates(t, stateStore, candidates)
	saveResumeTestCompletion(t, stateStore, candidates, now.Add(-time.Hour))

	decision, err := EvaluateTargetedScanResume(
		context.Background(), stateStore, candidates, 24*time.Hour, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Eligible || decision.Reason != "verified_recent_completion" {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.CompletedAt != now.Add(-time.Hour) {
		t.Fatalf("completed_at = %s", decision.CompletedAt)
	}
}

func TestEvaluateTargetedScanResumeRejectsUnsafeState(t *testing.T) {
	now := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		setup  func(*testing.T, *store.Store, []catalog.Item)
		reason string
	}{
		{
			name: "missing completion",
			setup: func(t *testing.T, stateStore *store.Store, candidates []catalog.Item) {
				saveResumeTestStates(t, stateStore, candidates)
			},
			reason: "completion_missing",
		},
		{
			name: "checkpoint present",
			setup: func(t *testing.T, stateStore *store.Store, candidates []catalog.Item) {
				saveResumeTestStates(t, stateStore, candidates)
				saveResumeTestCompletion(t, stateStore, candidates, now.Add(-time.Hour))
				fingerprint, err := TargetedCatalogFingerprint(candidates)
				if err != nil {
					t.Fatal(err)
				}
				if err := stateStore.SaveTargetedScanCheckpoint(context.Background(), store.TargetedScanCheckpoint{
					CatalogFingerprint: fingerprint,
					CompletedCount:     1,
					LastItemID:         candidates[0].ItemID,
				}); err != nil {
					t.Fatal(err)
				}
			},
			reason: "checkpoint_present",
		},
		{
			name: "expired completion",
			setup: func(t *testing.T, stateStore *store.Store, candidates []catalog.Item) {
				saveResumeTestStates(t, stateStore, candidates)
				saveResumeTestCompletion(t, stateStore, candidates, now.Add(-24*time.Hour))
			},
			reason: "completion_expired",
		},
		{
			name: "missing state",
			setup: func(t *testing.T, stateStore *store.Store, candidates []catalog.Item) {
				saveResumeTestState(t, stateStore, candidates[0])
				saveResumeTestCompletion(t, stateStore, candidates, now.Add(-time.Hour))
			},
			reason: "state_count_mismatch",
		},
		{
			name: "identity mismatch",
			setup: func(t *testing.T, stateStore *store.Store, candidates []catalog.Item) {
				saveResumeTestStates(t, stateStore, candidates)
				state, found, err := stateStore.GetItemState(context.Background(), candidates[1].ItemID)
				if err != nil || !found {
					t.Fatalf("state found=%t err=%v", found, err)
				}
				state.MarketHashName = "different"
				if err := stateStore.SaveItemState(context.Background(), state); err != nil {
					t.Fatal(err)
				}
				saveResumeTestCompletion(t, stateStore, candidates, now.Add(-time.Hour))
			},
			reason: "state_identity_mismatch",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			stateStore := openTestStore(t)
			candidates := resumeTestCandidates()
			testCase.setup(t, stateStore, candidates)
			decision, err := EvaluateTargetedScanResume(
				context.Background(), stateStore, candidates, 24*time.Hour, now,
			)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Eligible || decision.Reason != testCase.reason {
				t.Fatalf("decision = %#v, want reason %q", decision, testCase.reason)
			}
		})
	}
}

func TestRunWithRecentCompletionSkipsFullScanAndRunsPurchase(t *testing.T) {
	stateStore := openTestStore(t)
	candidate := catalog.Item{ItemID: 1, MarketHashName: "hash-1", NameCN: "一"}
	saveResumeTestState(t, stateStore, candidate)

	client, requests := runtimeTestClient(t, func(string, string, int) (int, string) {
		return http.StatusInternalServerError, `{}`
	})
	purchase := &countingPurchaseRunner{}
	config := runtimeTestConfig()
	config.Candidates = []catalog.Item{candidate}
	config.Purchase = purchase
	config.InitialFullScanCompletedAt = time.Now().UTC().Add(-time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	if err := Run(ctx, client, stateStore, config, nil); err != nil {
		t.Fatal(err)
	}
	if requests.total() != 0 {
		t.Fatalf("SMIS requests = %#v, want none", requests.snapshot())
	}
	if purchase.calls != 1 {
		t.Fatalf("purchase calls = %d, want 1", purchase.calls)
	}
}

func TestCompletedTargetedScanSavesResumeReceipt(t *testing.T) {
	stateStore := openTestStore(t)
	candidates := []catalog.Item{{ItemID: 1, MarketHashName: "hash-1", NameCN: "一"}}
	client, _ := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		if path == "/api/commodity/1" {
			return http.StatusOK, detailData(1, "0.59", 400, "2026-09-05 03:00:00")
		}
		return http.StatusInternalServerError, `{}`
	})

	if _, err := RunTargetedScanCycle(context.Background(), client, stateStore, candidates, 0); err != nil {
		t.Fatal(err)
	}
	completion, found, err := stateStore.GetTargetedScanCompletion(context.Background())
	if err != nil || !found {
		t.Fatalf("completion found=%t err=%v", found, err)
	}
	fingerprint, err := TargetedCatalogFingerprint(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if completion.CatalogFingerprint != fingerprint || completion.CandidateCount != 1 || completion.CompletedAt.IsZero() {
		t.Fatalf("completion = %#v", completion)
	}
}

func resumeTestCandidates() []catalog.Item {
	return []catalog.Item{
		{ItemID: 1, MarketHashName: "hash-1", NameCN: "一"},
		{ItemID: 2, MarketHashName: "hash-2", NameCN: "二"},
	}
}

func saveResumeTestStates(t *testing.T, stateStore *store.Store, candidates []catalog.Item) {
	t.Helper()
	for _, candidate := range candidates {
		saveResumeTestState(t, stateStore, candidate)
	}
}

func saveResumeTestState(t *testing.T, stateStore *store.Store, candidate catalog.Item) {
	t.Helper()
	if err := stateStore.SaveItemState(context.Background(), store.ItemState{
		ItemID:           candidate.ItemID,
		MarketHashName:   candidate.MarketHashName,
		NameCN:           candidate.NameCN,
		Ratio:            7000,
		SteamDailyVolume: 300,
		SteamPrice:       "1.00",
		Tier:             core.TierB3,
		NextCheckAt:      time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

func saveResumeTestCompletion(
	t *testing.T,
	stateStore *store.Store,
	candidates []catalog.Item,
	completedAt time.Time,
) {
	t.Helper()
	fingerprint, err := TargetedCatalogFingerprint(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.SaveTargetedScanCompletion(context.Background(), store.TargetedScanCompletion{
		CatalogFingerprint: fingerprint,
		CandidateCount:     len(candidates),
		CompletedAt:        completedAt,
	}); err != nil {
		t.Fatal(err)
	}
}
