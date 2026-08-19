package core

// RatioBP stores a ratio in ten-thousandths: 1.0000 equals 10000.
type RatioBP int32

type Tier string

const (
	TierA  Tier = "A"
	TierB1 Tier = "B1"
	TierB2 Tier = "B2"
	TierB3 Tier = "B3"
	TierC  Tier = "C"
)

type MarketData struct {
	Ratio            RatioBP
	SteamDailyVolume int
}

type TierChange struct {
	Previous *Tier
	Current  Tier
}

func (change TierChange) Changed() bool {
	return change.Previous == nil || *change.Previous != change.Current
}

func validTier(tier Tier) bool {
	switch tier {
	case TierA, TierB1, TierB2, TierB3, TierC:
		return true
	default:
		return false
	}
}
