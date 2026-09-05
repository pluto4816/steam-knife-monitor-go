package app

import (
	"context"
	"fmt"

	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type OneShotResult struct {
	Scan          ScanResult
	DueChecks     DueCheckResult
	TierCounts    store.ItemTierCounts
	Purchases     PurchaseRunResult
	Notifications SendResult
}

func RunOneShot(
	ctx context.Context,
	client *collector.SMISClient,
	stateStore *store.Store,
	config RunConfig,
	sender Sender,
) (OneShotResult, error) {
	var result OneShotResult
	if err := validateRunConfig(config); err != nil {
		return result, err
	}

	var err error
	result.Scan, err = runConfiguredScan(ctx, client, stateStore, config)
	if err != nil {
		return result, fmt.Errorf("full scan: %w", err)
	}
	if err := waitContext(ctx, config.RequestDelay); err != nil {
		return result, fmt.Errorf("wait before due checks: %w", err)
	}

	result.DueChecks, err = runConfiguredDueChecks(ctx, client, stateStore, config)
	if err != nil {
		return result, fmt.Errorf("due checks: %w", err)
	}
	result.TierCounts, err = countConfiguredItemStatesByTier(ctx, stateStore, config)
	if err != nil {
		return result, fmt.Errorf("summarize item states: %w", err)
	}

	var purchaseErr error
	if config.Purchase != nil {
		result.Purchases, purchaseErr = config.Purchase.Run(ctx)
		logPurchaseRunResult("C5 purchase phase complete", result.Purchases)
	}
	if purchaseErr == nil && result.Purchases.PlacedPurchase != nil {
		if err := SendC5PurchaseReceipt(ctx, sender, *result.Purchases.PlacedPurchase); err != nil {
			return result, err
		}
		return result, nil
	}

	if sender != nil {
		result.Notifications, err = SendPendingAlerts(ctx, stateStore, sender)
		if err != nil {
			return result, fmt.Errorf("send pending alerts: %w", err)
		}
	} else {
		alerts, err := stateStore.ListPendingAlerts(ctx)
		if err != nil {
			return result, fmt.Errorf("list preview alerts: %w", err)
		}
		result.Notifications.PendingCount = len(alerts)
	}
	if purchaseErr != nil {
		return result, fmt.Errorf("C5 purchase phase: %w", purchaseErr)
	}
	return result, nil
}

func countConfiguredItemStatesByTier(
	ctx context.Context,
	stateStore *store.Store,
	config RunConfig,
) (store.ItemTierCounts, error) {
	if config.Candidates == nil {
		return stateStore.CountItemStatesByTier(ctx)
	}

	var counts store.ItemTierCounts
	for _, candidate := range config.Candidates {
		state, found, err := stateStore.GetItemState(ctx, candidate.ItemID)
		if err != nil {
			return store.ItemTierCounts{}, err
		}
		if !found {
			return store.ItemTierCounts{}, fmt.Errorf("candidate item %d has no state after scan", candidate.ItemID)
		}
		switch state.Tier {
		case core.TierA:
			counts.A++
		case core.TierB1:
			counts.B1++
		case core.TierB2:
			counts.B2++
		case core.TierB3:
			counts.B3++
		case core.TierC:
			counts.C++
		default:
			return store.ItemTierCounts{}, fmt.Errorf("candidate item %d has invalid tier %q", candidate.ItemID, state.Tier)
		}
	}
	return counts, nil
}
