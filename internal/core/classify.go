package core

import "fmt"

const (
	aRatioMax      RatioBP = 6000
	aVolumeMin             = 350
	b1RatioMax     RatioBP = 6300
	b2RatioMax     RatioBP = 6800
	b3RatioMax     RatioBP = 7500
	bVolumeMin             = 250
	aKeepRatioMax  RatioBP = 6100
	aKeepVolumeMin         = 320
)

func BaseClassify(market MarketData) (Tier, error) {
	if err := validateMarketData(market); err != nil {
		return "", err
	}
	return baseClassifyValid(market), nil
}

// ResolveTier applies A hysteresis only when the previous tier is A.
func ResolveTier(previous *Tier, market MarketData) (TierChange, error) {
	if err := validateMarketData(market); err != nil {
		return TierChange{}, err
	}
	if previous != nil && !validTier(*previous) {
		return TierChange{}, fmt.Errorf("invalid previous tier %q", *previous)
	}

	current := Tier("")
	if previous != nil && *previous == TierA &&
		market.Ratio <= aKeepRatioMax &&
		market.SteamDailyVolume >= aKeepVolumeMin {
		current = TierA
	} else {
		current = baseClassifyValid(market)
	}
	return TierChange{Previous: previous, Current: current}, nil
}

func baseClassifyValid(market MarketData) Tier {
	switch {
	case market.Ratio <= aRatioMax && market.SteamDailyVolume >= aVolumeMin:
		return TierA
	case market.Ratio <= b1RatioMax && market.SteamDailyVolume >= bVolumeMin:
		return TierB1
	case market.Ratio <= b2RatioMax && market.SteamDailyVolume >= bVolumeMin:
		return TierB2
	case market.Ratio <= b3RatioMax && market.SteamDailyVolume >= bVolumeMin:
		return TierB3
	default:
		return TierC
	}
}

func validateMarketData(market MarketData) error {
	if market.Ratio < 0 {
		return fmt.Errorf("invalid ratio: value is negative")
	}
	if market.SteamDailyVolume < 0 {
		return fmt.Errorf("invalid steam daily volume: value is negative")
	}
	return nil
}
