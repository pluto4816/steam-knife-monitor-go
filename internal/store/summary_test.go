package store

import (
	"context"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

func TestCountItemStatesByTier(t *testing.T) {
	stateStore := openTestStore(t)
	tiers := []core.Tier{
		core.TierA,
		core.TierA,
		core.TierB1,
		core.TierB2,
		core.TierB3,
		core.TierC,
		core.TierC,
		core.TierC,
	}
	for index, tier := range tiers {
		if err := stateStore.SaveItemState(context.Background(), ItemState{
			ItemID:           int64(index + 1),
			MarketHashName:   "hash",
			NameCN:           "name",
			Ratio:            7000,
			SteamDailyVolume: 300,
			SteamPrice:       "10.00",
			Tier:             tier,
			NextCheckAt:      time.Now().UTC().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	counts, err := stateStore.CountItemStatesByTier(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := ItemTierCounts{A: 2, B1: 1, B2: 1, B3: 1, C: 3}
	if counts != want {
		t.Fatalf("counts = %#v, want %#v", counts, want)
	}
}

func TestCountItemStatesByTierEmpty(t *testing.T) {
	counts, err := openTestStore(t).CountItemStatesByTier(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts != (ItemTierCounts{}) {
		t.Fatalf("counts = %#v, want zero", counts)
	}
}
