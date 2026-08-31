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
	return runDueChecks(ctx, client, stateStore, requestDelay, nil)
}

func runConfiguredDueChecks(
	ctx context.Context,
	client *collector.SMISClient,
	stateStore *store.Store,
	config RunConfig,
) (DueCheckResult, error) {
	if config.Candidates == nil {
		return RunDueChecks(ctx, client, stateStore, config.RequestDelay)
	}
	expectedHashNames := make(map[int64]string, len(config.Candidates))
	for index, candidate := range config.Candidates {
		if candidate.ItemID <= 0 || candidate.MarketHashName == "" {
			return DueCheckResult{}, fmt.Errorf("candidate %d is invalid", index)
		}
		if _, exists := expectedHashNames[candidate.ItemID]; exists {
			return DueCheckResult{}, fmt.Errorf("candidate item ID %d is duplicated", candidate.ItemID)
		}
		expectedHashNames[candidate.ItemID] = candidate.MarketHashName
	}
	return runDueChecks(ctx, client, stateStore, config.RequestDelay, expectedHashNames)
}

func runDueChecks(
	ctx context.Context,
	client *collector.SMISClient,
	stateStore *store.Store,
	requestDelay time.Duration,
	expectedHashNames map[int64]string,
) (DueCheckResult, error) {
	dueItems, err := stateStore.ListDueItemStates(ctx, time.Now().UTC())
	if err != nil {
		return DueCheckResult{}, fmt.Errorf("list due items: %w", err)
	}
	if expectedHashNames != nil {
		filtered := make([]store.ItemState, 0, len(dueItems))
		for _, dueItem := range dueItems {
			if _, exists := expectedHashNames[dueItem.ItemID]; exists {
				filtered = append(filtered, dueItem)
			}
		}
		dueItems = filtered
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
		if expectedHashNames != nil && marketItem.MarketHashName != expectedHashNames[dueItem.ItemID] {
			return result, fmt.Errorf(
				"due item %d hash name %q does not match catalog %q",
				dueItem.ItemID,
				marketItem.MarketHashName,
				expectedHashNames[dueItem.ItemID],
			)
		}
		checkedAt := time.Now().UTC()
		if _, err := ProcessMarketItem(ctx, stateStore, marketItem, checkedAt); err != nil {
			return result, fmt.Errorf("process due item %d: %w", dueItem.ItemID, err)
		}
		result.ProcessedCount++
	}
	return result, nil
}
