package app

import (
	"context"
	"fmt"

	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
)

type SteamPriceSource interface {
	FetchLowestPrice(context.Context, string) (string, error)
}

func applyOfficialSteamPrice(
	ctx context.Context,
	source SteamPriceSource,
	item collector.MarketItem,
) (collector.MarketItem, error) {
	if source == nil {
		return collector.MarketItem{}, fmt.Errorf("Steam price source cannot be nil")
	}
	steamPrice, err := source.FetchLowestPrice(ctx, item.MarketHashName)
	if err != nil {
		return collector.MarketItem{}, fmt.Errorf(
			"fetch Steam lowest price for %q: %w",
			item.MarketHashName,
			err,
		)
	}
	ratio, err := collector.CalculateRatioBP(steamPrice, item.LowestPlatformPrice)
	if err != nil {
		return collector.MarketItem{}, fmt.Errorf(
			"recalculate ratio for item %d with Steam lowest price: %w",
			item.ItemID,
			err,
		)
	}
	item.SteamPrice = steamPrice
	item.MarketData.Ratio = ratio
	return item, nil
}
