package app

import (
	"context"
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
	requestMade := false
	var previousID int64
	for index, candidate := range ordered {
		if candidate.ItemID <= 0 || candidate.MarketHashName == "" {
			return result, fmt.Errorf("candidate %d is invalid", index)
		}
		if index > 0 && candidate.ItemID == previousID {
			return result, fmt.Errorf("candidate item ID %d is duplicated", candidate.ItemID)
		}
		previousID = candidate.ItemID

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
	}
	return result, nil
}
