package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTargetedScanCompletionLifecycle(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	if _, found, err := stateStore.GetTargetedScanCompletion(ctx); err != nil || found {
		t.Fatalf("initial completion found=%t err=%v", found, err)
	}

	completedAt := time.Date(2026, 9, 5, 3, 4, 20, 123000000, time.UTC)
	first := TargetedScanCompletion{
		CatalogFingerprint: strings.Repeat("a", 64),
		CandidateCount:     426,
		CompletedAt:        completedAt,
	}
	if err := stateStore.SaveTargetedScanCompletion(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, found, err := stateStore.GetTargetedScanCompletion(ctx)
	if err != nil || !found || got != first {
		t.Fatalf("completion=%#v found=%t err=%v, want %#v", got, found, err, first)
	}

	second := TargetedScanCompletion{
		CatalogFingerprint: strings.Repeat("b", 64),
		CandidateCount:     427,
		CompletedAt:        completedAt.Add(time.Hour),
	}
	if err := stateStore.SaveTargetedScanCompletion(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, found, err = stateStore.GetTargetedScanCompletion(ctx)
	if err != nil || !found || got != second {
		t.Fatalf("updated completion=%#v found=%t err=%v, want %#v", got, found, err, second)
	}
}

func TestTargetedScanCompletionRejectsInvalidValues(t *testing.T) {
	stateStore := openTestStore(t)
	valid := TargetedScanCompletion{
		CatalogFingerprint: strings.Repeat("a", 64),
		CandidateCount:     1,
		CompletedAt:        time.Unix(1, 0).UTC(),
	}
	tests := []struct {
		name       string
		completion TargetedScanCompletion
	}{
		{"short fingerprint", TargetedScanCompletion{CatalogFingerprint: "abc", CandidateCount: 1, CompletedAt: valid.CompletedAt}},
		{"zero candidate count", TargetedScanCompletion{CatalogFingerprint: valid.CatalogFingerprint, CompletedAt: valid.CompletedAt}},
		{"zero completion time", TargetedScanCompletion{CatalogFingerprint: valid.CatalogFingerprint, CandidateCount: 1}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if err := stateStore.SaveTargetedScanCompletion(context.Background(), testCase.completion); err == nil {
				t.Fatal("invalid completion was accepted")
			}
		})
	}
}
