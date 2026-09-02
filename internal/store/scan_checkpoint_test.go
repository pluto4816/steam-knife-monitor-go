package store

import (
	"context"
	"strings"
	"testing"
)

func TestTargetedScanCheckpointLifecycle(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	firstFingerprint := strings.Repeat("a", 64)
	secondFingerprint := strings.Repeat("b", 64)

	if _, found, err := stateStore.GetTargetedScanCheckpoint(ctx); err != nil || found {
		t.Fatalf("initial checkpoint found=%t err=%v", found, err)
	}
	first := TargetedScanCheckpoint{
		CatalogFingerprint: firstFingerprint,
		CompletedCount:     74,
		LastItemID:         1648,
	}
	if err := stateStore.SaveTargetedScanCheckpoint(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, found, err := stateStore.GetTargetedScanCheckpoint(ctx)
	if err != nil || !found || got != first {
		t.Fatalf("checkpoint=%#v found=%t err=%v, want %#v", got, found, err, first)
	}

	second := TargetedScanCheckpoint{
		CatalogFingerprint: secondFingerprint,
		CompletedCount:     3,
		LastItemID:         99,
	}
	if err := stateStore.SaveTargetedScanCheckpoint(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.ClearTargetedScanCheckpoint(ctx, firstFingerprint); err != nil {
		t.Fatal(err)
	}
	got, found, err = stateStore.GetTargetedScanCheckpoint(ctx)
	if err != nil || !found || got != second {
		t.Fatalf("checkpoint after mismatched clear=%#v found=%t err=%v", got, found, err)
	}
	if err := stateStore.ClearTargetedScanCheckpoint(ctx, secondFingerprint); err != nil {
		t.Fatal(err)
	}
	if _, found, err := stateStore.GetTargetedScanCheckpoint(ctx); err != nil || found {
		t.Fatalf("cleared checkpoint found=%t err=%v", found, err)
	}
}

func TestTargetedScanCheckpointRejectsInvalidValues(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	valid := TargetedScanCheckpoint{
		CatalogFingerprint: strings.Repeat("a", 64),
		CompletedCount:     1,
		LastItemID:         1,
	}
	tests := []struct {
		name       string
		checkpoint TargetedScanCheckpoint
	}{
		{"short fingerprint", TargetedScanCheckpoint{CatalogFingerprint: "abc", CompletedCount: 1, LastItemID: 1}},
		{"non-hex fingerprint", TargetedScanCheckpoint{CatalogFingerprint: strings.Repeat("z", 64), CompletedCount: 1, LastItemID: 1}},
		{"zero completed count", TargetedScanCheckpoint{CatalogFingerprint: valid.CatalogFingerprint, LastItemID: 1}},
		{"zero last item ID", TargetedScanCheckpoint{CatalogFingerprint: valid.CatalogFingerprint, CompletedCount: 1}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if err := stateStore.SaveTargetedScanCheckpoint(ctx, testCase.checkpoint); err == nil {
				t.Fatal("invalid checkpoint was accepted")
			}
		})
	}
}
