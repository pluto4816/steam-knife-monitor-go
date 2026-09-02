package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

func RunTargetedScanCycle(
	ctx context.Context,
	client *collector.SMISClient,
	stateStore *store.Store,
	candidates []catalog.Item,
	requestDelay time.Duration,
) (ScanResult, error) {
	result := ScanResult{CandidateCount: len(candidates)}
	if requestDelay < 0 {
		return result, fmt.Errorf("targeted scan request delay cannot be negative")
	}
	if len(candidates) == 0 {
		return result, fmt.Errorf("candidate catalog is empty")
	}

	ordered := append([]catalog.Item(nil), candidates...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].ItemID < ordered[right].ItemID
	})
	var previousID int64
	for index, candidate := range ordered {
		if candidate.ItemID <= 0 || candidate.MarketHashName == "" {
			return result, fmt.Errorf("candidate %d is invalid", index)
		}
		if index > 0 && candidate.ItemID == previousID {
			return result, fmt.Errorf("candidate item ID %d is duplicated", candidate.ItemID)
		}
		previousID = candidate.ItemID
	}

	fingerprint, err := targetedCatalogFingerprint(ordered)
	if err != nil {
		return result, err
	}
	startIndex := 0
	checkpoint, found, err := stateStore.GetTargetedScanCheckpoint(ctx)
	if err != nil {
		return result, err
	}
	if found && checkpoint.CatalogFingerprint != fingerprint {
		if err := stateStore.ClearTargetedScanCheckpoint(ctx, checkpoint.CatalogFingerprint); err != nil {
			return result, fmt.Errorf("invalidate targeted scan checkpoint: %w", err)
		}
		found = false
	}
	if found {
		if checkpoint.CompletedCount > len(ordered) {
			return result, fmt.Errorf(
				"targeted scan checkpoint completed count %d exceeds catalog count %d",
				checkpoint.CompletedCount,
				len(ordered),
			)
		}
		if ordered[checkpoint.CompletedCount-1].ItemID != checkpoint.LastItemID {
			return result, fmt.Errorf(
				"targeted scan checkpoint last item %d does not match catalog item %d at completed count %d",
				checkpoint.LastItemID,
				ordered[checkpoint.CompletedCount-1].ItemID,
				checkpoint.CompletedCount,
			)
		}
		startIndex = checkpoint.CompletedCount
		result.ResumedCount = startIndex
	}

	requestMade := false
	for index := startIndex; index < len(ordered); index++ {
		candidate := ordered[index]

		if err := waitBeforeRequest(ctx, requestDelay, requestMade); err != nil {
			return result, fmt.Errorf("wait before detail item %d: %w", candidate.ItemID, err)
		}
		marketItem, err := client.FetchMarketItem(ctx, candidate.ItemID)
		requestMade = true
		if err != nil {
			return result, fmt.Errorf("targeted scan incomplete at item %d: %w", candidate.ItemID, err)
		}
		if marketItem.MarketHashName != candidate.MarketHashName {
			return result, fmt.Errorf(
				"targeted scan item %d hash name %q does not match catalog %q",
				candidate.ItemID,
				marketItem.MarketHashName,
				candidate.MarketHashName,
			)
		}
		result.MatchedCount++
		checkedAt := time.Now().UTC()
		if _, err := ProcessMarketItem(ctx, stateStore, marketItem, checkedAt); err != nil {
			return result, fmt.Errorf("process targeted item %d: %w", candidate.ItemID, err)
		}
		result.ProcessedCount++
		if err := stateStore.SaveTargetedScanCheckpoint(ctx, store.TargetedScanCheckpoint{
			CatalogFingerprint: fingerprint,
			CompletedCount:     index + 1,
			LastItemID:         candidate.ItemID,
		}); err != nil {
			return result, fmt.Errorf("save targeted scan progress after item %d: %w", candidate.ItemID, err)
		}
	}
	if err := stateStore.ClearTargetedScanCheckpoint(ctx, fingerprint); err != nil {
		return result, fmt.Errorf("clear completed targeted scan checkpoint: %w", err)
	}
	return result, nil
}

func targetedCatalogFingerprint(candidates []catalog.Item) (string, error) {
	encoded, err := json.Marshal(candidates)
	if err != nil {
		return "", fmt.Errorf("encode targeted candidate catalog fingerprint: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
