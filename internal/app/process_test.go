package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

var testCheckedAt = time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)

func TestProcessMarketItemFirstObservation(t *testing.T) {
	stateStore := openTestStore(t)
	item := marketItem(53, 6000, 350)

	result, err := ProcessMarketItem(context.Background(), stateStore, item, testCheckedAt)
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
	if result.NextCheckAt != testCheckedAt.Add(5*time.Minute) {
		t.Fatalf("NextCheckAt = %s, want 15:05", result.NextCheckAt)
	}
	assertStoredItem(t, stateStore, item, core.TierA, result.NextCheckAt)
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

	result, err := ProcessMarketItem(context.Background(), stateStore, item, testCheckedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !result.HadPrevious || result.PreviousTier != core.TierB1 || result.CurrentTier != core.TierA {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.RatioDrop != 350 || !result.FastDrop {
		t.Fatalf("trend = drop %d fast=%t, want 350 true", result.RatioDrop, result.FastDrop)
	}
	if result.NextCheckAt != testCheckedAt.Add(5*time.Minute) {
		t.Fatalf("fast A NextCheckAt = %s, want 15:05", result.NextCheckAt)
	}
	assertStoredItem(t, stateStore, item, core.TierA, result.NextCheckAt)
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
	result, err := ProcessMarketItem(context.Background(), second, item, testCheckedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !result.HadPrevious || result.PreviousTier != core.TierA || result.CurrentTier != core.TierA {
		t.Fatalf("A hysteresis result: %#v", result)
	}
	if result.RatioDrop != -150 || result.FastDrop {
		t.Fatalf("trend = drop %d fast=%t, want -150 false", result.RatioDrop, result.FastDrop)
	}
	if result.NextCheckAt != testCheckedAt.Add(5*time.Minute) {
		t.Fatalf("A retention NextCheckAt = %s, want 15:05", result.NextCheckAt)
	}
	assertStoredItem(t, second, item, core.TierA, result.NextCheckAt)
}

func TestProcessMarketItemLeavesA(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, store.ItemState{
		ItemID: 53, Ratio: 6000, SteamDailyVolume: 350, Tier: core.TierA,
	})
	item := marketItem(53, 6200, 319)

	result, err := ProcessMarketItem(context.Background(), stateStore, item, testCheckedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousTier != core.TierA || result.CurrentTier != core.TierB1 {
		t.Fatalf("A exit result: %#v", result)
	}
	if result.NextCheckAt != testCheckedAt.Add(10*time.Minute) {
		t.Fatalf("B1 NextCheckAt = %s, want 15:10", result.NextCheckAt)
	}
	assertStoredItem(t, stateStore, item, core.TierB1, result.NextCheckAt)
}

func TestProcessMarketItemRatioDoesNotImprove(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, store.ItemState{
		ItemID: 53, Ratio: 6200, SteamDailyVolume: 300, Tier: core.TierB1,
	})
	item := marketItem(53, 6300, 250)

	result, err := ProcessMarketItem(context.Background(), stateStore, item, testCheckedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.RatioDrop != -100 || result.FastDrop {
		t.Fatalf("trend = drop %d fast=%t, want -100 false", result.RatioDrop, result.FastDrop)
	}
	if result.NextCheckAt != testCheckedAt.Add(10*time.Minute) {
		t.Fatalf("B1 NextCheckAt = %s, want 15:10", result.NextCheckAt)
	}
}

func TestProcessMarketItemFirstB1Schedule(t *testing.T) {
	stateStore := openTestStore(t)
	result, err := ProcessMarketItem(
		context.Background(), stateStore, marketItem(53, 6200, 300), testCheckedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.CurrentTier != core.TierB1 || result.FastDrop {
		t.Fatalf("result = %#v, want first B1 without fast drop", result)
	}
	if result.NextCheckAt != testCheckedAt.Add(10*time.Minute) {
		t.Fatalf("NextCheckAt = %s, want 15:10", result.NextCheckAt)
	}
}

func TestProcessMarketItemFastDropB2Schedule(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, store.ItemState{
		ItemID: 53, Ratio: 7000, SteamDailyVolume: 300, Tier: core.TierC,
	})
	result, err := ProcessMarketItem(
		context.Background(), stateStore, marketItem(53, 6700, 300), testCheckedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.CurrentTier != core.TierB2 || result.RatioDrop != 300 || !result.FastDrop {
		t.Fatalf("result = %#v, want fast-drop B2", result)
	}
	if result.NextCheckAt != testCheckedAt.Add(15*time.Minute) {
		t.Fatalf("NextCheckAt = %s, want 15:15", result.NextCheckAt)
	}
	assertStoredItem(t, stateStore, marketItem(53, 6700, 300), core.TierB2, result.NextCheckAt)
}

func TestProcessMarketItemRejectsZeroCheckedAt(t *testing.T) {
	stateStore := openTestStore(t)
	if _, err := ProcessMarketItem(
		context.Background(), stateStore, marketItem(53, 6000, 350), time.Time{},
	); err == nil {
		t.Fatal("zero checkedAt was accepted")
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
	if _, err := ProcessMarketItem(context.Background(), stateStore, marketItem(53, 6000, 350), testCheckedAt); err == nil {
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
	if state.NextCheckAt.IsZero() {
		state.NextCheckAt = testCheckedAt.Add(-time.Minute)
	}
	if err := stateStore.SaveItemState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
}

func assertStoredItem(
	t *testing.T,
	stateStore *store.Store,
	item collector.MarketItem,
	tier core.Tier,
	nextCheckAt time.Time,
) {
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
		NextCheckAt:      nextCheckAt,
	}
	if !found || got != want {
		t.Fatalf("stored state = %#v, found=%t, want %#v", got, found, want)
	}
}
