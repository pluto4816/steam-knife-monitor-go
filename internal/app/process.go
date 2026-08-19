package app

import (
	"context"
	"fmt"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type ProcessResult struct {
	HadPrevious  bool
	PreviousTier core.Tier
	CurrentTier  core.Tier
	RatioDrop    core.RatioBP
	FastDrop     bool
	NextCheckAt  time.Time
}

func ProcessMarketItem(
	ctx context.Context,
	stateStore *store.Store,
	item collector.MarketItem,
	checkedAt time.Time,
) (ProcessResult, error) {
	if checkedAt.IsZero() {
		return ProcessResult{}, fmt.Errorf("checkedAt cannot be zero")
	}
	checkedAt = time.UnixMilli(checkedAt.UTC().UnixMilli()).UTC()

	previous, found, err := stateStore.GetItemState(ctx, item.ItemID)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("get previous state for item %d: %w", item.ItemID, err)
	}

	var previousTier *core.Tier
	if found {
		previousTier = &previous.Tier
	}
	change, err := core.ResolveTier(previousTier, item.MarketData)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("resolve tier for item %d: %w", item.ItemID, err)
	}

	result := ProcessResult{
		HadPrevious: found,
		CurrentTier: change.Current,
	}
	if found {
		result.PreviousTier = previous.Tier
		result.RatioDrop, err = core.RatioDrop(previous.Ratio, item.MarketData.Ratio)
		if err != nil {
			return ProcessResult{}, fmt.Errorf("calculate ratio drop for item %d: %w", item.ItemID, err)
		}
		result.FastDrop = core.IsFastDrop(result.RatioDrop)
	}
	interval, err := core.CheckInterval(result.CurrentTier, result.FastDrop)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("calculate check interval for item %d: %w", item.ItemID, err)
	}
	result.NextCheckAt = checkedAt.Add(interval).UTC()

	if err := stateStore.SaveItemState(ctx, store.ItemState{
		ItemID:           item.ItemID,
		MarketHashName:   item.MarketHashName,
		NameCN:           item.NameCN,
		Ratio:            item.MarketData.Ratio,
		SteamDailyVolume: item.MarketData.SteamDailyVolume,
		SteamPrice:       item.SteamPrice,
		Tier:             change.Current,
		NextCheckAt:      result.NextCheckAt,
	}); err != nil {
		return ProcessResult{}, fmt.Errorf("save current state for item %d: %w", item.ItemID, err)
	}
	return result, nil
}
