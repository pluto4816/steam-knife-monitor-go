package app

import (
	"context"
	"fmt"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type DueCheckResult struct {
	DueCount       int
	ProcessedCount int
}

func RunDueChecks(
	ctx context.Context,
	client *collector.SMISClient,
	stateStore *store.Store,
	requestDelay time.Duration,
) (DueCheckResult, error) {
	dueItems, err := stateStore.ListDueItemStates(ctx, time.Now().UTC())
	if err != nil {
		return DueCheckResult{}, fmt.Errorf("list due items: %w", err)
	}
	result := DueCheckResult{DueCount: len(dueItems)}
	requestMade := false

	for _, dueItem := range dueItems {
		if err := waitBeforeRequest(ctx, requestDelay, requestMade); err != nil {
			return result, fmt.Errorf("wait before detail item %d: %w", dueItem.ItemID, err)
		}
		marketItem, err := client.FetchMarketItem(ctx, dueItem.ItemID)
		requestMade = true
		if err != nil {
			return result, fmt.Errorf("fetch detail item %d: %w", dueItem.ItemID, err)
		}
		checkedAt := time.Now().UTC()
		if _, err := ProcessMarketItem(ctx, stateStore, marketItem, checkedAt); err != nil {
			return result, fmt.Errorf("process due item %d: %w", dueItem.ItemID, err)
		}
		result.ProcessedCount++
	}
	return result, nil
}
