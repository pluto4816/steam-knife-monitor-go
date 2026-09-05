package app

import (
	"context"
	"fmt"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type TargetedScanResumeDecision struct {
	Eligible           bool
	Reason             string
	CatalogFingerprint string
	CompletedAt        time.Time
}

func EvaluateTargetedScanResume(
	ctx context.Context,
	stateStore *store.Store,
	candidates []catalog.Item,
	maxAge time.Duration,
	now time.Time,
) (TargetedScanResumeDecision, error) {
	var decision TargetedScanResumeDecision
	if stateStore == nil {
		return decision, fmt.Errorf("targeted scan resume store cannot be nil")
	}
	if len(candidates) == 0 {
		return decision, fmt.Errorf("targeted scan resume candidates cannot be empty")
	}
	if maxAge <= 0 {
		return decision, fmt.Errorf("targeted scan resume max age must be positive")
	}
	if now.IsZero() {
		return decision, fmt.Errorf("targeted scan resume time cannot be zero")
	}
	now = now.UTC()

	fingerprint, err := TargetedCatalogFingerprint(candidates)
	if err != nil {
		return decision, err
	}
	decision.CatalogFingerprint = fingerprint

	_, checkpointFound, err := stateStore.GetTargetedScanCheckpoint(ctx)
	if err != nil {
		return decision, err
	}
	if checkpointFound {
		decision.Reason = "checkpoint_present"
		return decision, nil
	}

	completion, completionFound, err := stateStore.GetTargetedScanCompletion(ctx)
	if err != nil {
		return decision, err
	}
	if !completionFound {
		decision.Reason = "completion_missing"
		return decision, nil
	}
	decision.CompletedAt = completion.CompletedAt
	if completion.CatalogFingerprint != fingerprint {
		decision.Reason = "catalog_fingerprint_mismatch"
		return decision, nil
	}
	if completion.CandidateCount != len(candidates) {
		decision.Reason = "candidate_count_mismatch"
		return decision, nil
	}
	if completion.CompletedAt.After(now) {
		decision.Reason = "completion_in_future"
		return decision, nil
	}
	if now.Sub(completion.CompletedAt) >= maxAge {
		decision.Reason = "completion_expired"
		return decision, nil
	}

	counts, err := stateStore.CountItemStatesByTier(ctx)
	if err != nil {
		return decision, err
	}
	stateCount := counts.A + counts.B1 + counts.B2 + counts.B3 + counts.C
	if stateCount != len(candidates) {
		decision.Reason = "state_count_mismatch"
		return decision, nil
	}
	for _, candidate := range candidates {
		state, found, err := stateStore.GetItemState(ctx, candidate.ItemID)
		if err != nil {
			return decision, err
		}
		if !found {
			decision.Reason = "state_missing"
			return decision, nil
		}
		if state.MarketHashName != candidate.MarketHashName || state.NameCN != candidate.NameCN {
			decision.Reason = "state_identity_mismatch"
			return decision, nil
		}
	}

	decision.Eligible = true
	decision.Reason = "verified_recent_completion"
	return decision, nil
}
