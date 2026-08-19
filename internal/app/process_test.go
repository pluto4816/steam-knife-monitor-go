package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

func TestProcessMarketItemFirstObservation(t *testing.T) {
	stateStore := openTestStore(t)
	item := marketItem(53, 6000, 350)

	result, err := ProcessMarketItem(context.Background(), stateStore, item)
	if err != nil {
		t.Fatal(err)
	}
	if result.HadPrevious {
		t.Fatal("first observation unexpectedly had previous state")
	}
	if result.PreviousTier != "" || result.CurrentTier != core.TierA {
		t.Fatalf("tiers = %q -> %q, want no previous -> A", result.PreviousTier, result.CurrentTier)
	}
	if result.RatioDrop != 0 || result.FastDrop {
		t.Fatalf("first observation trend = drop %d fast=%t", result.RatioDrop, result.FastDrop)
	}
	assertStoredItem(t, stateStore, item, core.TierA)
}

func TestProcessMarketItemNonAToAAndFastDrop(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, store.ItemState{
		ItemID:           53,
		MarketHashName:   "old hash",
		NameCN:           "旧名称",
		Ratio:            6250,
		SteamDailyVolume: 300,
		SteamPrice:       "20.00",
		Tier:             core.TierB1,
	})
	item := marketItem(53, 5900, 400)
	item.MarketHashName = "current hash"
	item.NameCN = "当前名称"
	item.SteamPrice = "17.7300"

	result, err := ProcessMarketItem(context.Background(), stateStore, item)
	if err != nil {
		t.Fatal(err)
	}
	if !result.HadPrevious || result.PreviousTier != core.TierB1 || result.CurrentTier != core.TierA {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.RatioDrop != 350 || !result.FastDrop {
		t.Fatalf("trend = drop %d fast=%t, want 350 true", result.RatioDrop, result.FastDrop)
	}
	assertStoredItem(t, stateStore, item, core.TierA)
}

func TestProcessMarketItemAHysteresisUsesStateAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saveState(t, first, store.ItemState{
		ItemID:           53,
		MarketHashName:   "AK-47 | Elite Build (Field-Tested)",
		NameCN:           "AK-47 | 精英之作 (久经沙场)",
		Ratio:            5900,
		SteamDailyVolume: 350,
		SteamPrice:       "17.73",
		Tier:             core.TierA,
	})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	item := marketItem(53, 6050, 320)
	result, err := ProcessMarketItem(context.Background(), second, item)
	if err != nil {
		t.Fatal(err)
	}
	if !result.HadPrevious || result.PreviousTier != core.TierA || result.CurrentTier != core.TierA {
		t.Fatalf("A hysteresis result: %#v", result)
	}
	if result.RatioDrop != -150 || result.FastDrop {
		t.Fatalf("trend = drop %d fast=%t, want -150 false", result.RatioDrop, result.FastDrop)
	}
	assertStoredItem(t, second, item, core.TierA)
}

func TestProcessMarketItemLeavesA(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, store.ItemState{
		ItemID: 53, Ratio: 6000, SteamDailyVolume: 350, Tier: core.TierA,
	})
	item := marketItem(53, 6200, 319)

	result, err := ProcessMarketItem(context.Background(), stateStore, item)
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousTier != core.TierA || result.CurrentTier != core.TierB1 {
		t.Fatalf("A exit result: %#v", result)
	}
	assertStoredItem(t, stateStore, item, core.TierB1)
}

func TestProcessMarketItemRatioDoesNotImprove(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, store.ItemState{
		ItemID: 53, Ratio: 6200, SteamDailyVolume: 300, Tier: core.TierB1,
	})
	item := marketItem(53, 6300, 250)

	result, err := ProcessMarketItem(context.Background(), stateStore, item)
	if err != nil {
		t.Fatal(err)
	}
	if result.RatioDrop != -100 || result.FastDrop {
		t.Fatalf("trend = drop %d fast=%t, want -100 false", result.RatioDrop, result.FastDrop)
	}
}

func TestProcessMarketItemReturnsDatabaseError(t *testing.T) {
	stateStore, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessMarketItem(context.Background(), stateStore, marketItem(53, 6000, 350)); err == nil {
		t.Fatal("processing with a closed Store unexpectedly succeeded")
	}
}

func marketItem(itemID int64, ratio core.RatioBP, volume int) collector.MarketItem {
	return collector.MarketItem{
		ItemID:         itemID,
		MarketHashName: "AK-47 | Elite Build (Field-Tested)",
		NameCN:         "AK-47 | 精英之作 (久经沙场)",
		MarketData: core.MarketData{
			Ratio:            ratio,
			SteamDailyVolume: volume,
		},
		SteamPrice: "17.73",
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	stateStore, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stateStore.Close(); err != nil {
			t.Errorf("close Store: %v", err)
		}
	})
	return stateStore
}

func saveState(t *testing.T, stateStore *store.Store, state store.ItemState) {
	t.Helper()
	if err := stateStore.SaveItemState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
}

func assertStoredItem(t *testing.T, stateStore *store.Store, item collector.MarketItem, tier core.Tier) {
	t.Helper()
	got, found, err := stateStore.GetItemState(context.Background(), item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	want := store.ItemState{
		ItemID:           item.ItemID,
		MarketHashName:   item.MarketHashName,
		NameCN:           item.NameCN,
		Ratio:            item.MarketData.Ratio,
		SteamDailyVolume: item.MarketData.SteamDailyVolume,
		SteamPrice:       item.SteamPrice,
		Tier:             tier,
	}
	if !found || got != want {
		t.Fatalf("stored state = %#v, found=%t, want %#v", got, found, want)
	}
}
