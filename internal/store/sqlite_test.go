package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

func TestOpenInitializesSchemaAndCanReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitor.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var tableName string
	if err := store.db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='item_state'",
	).Scan(&tableName); err != nil {
		t.Fatalf("item_state table was not created: %v", err)
	}
	if tableName != "item_state" {
		t.Fatalf("table = %q, want item_state", tableName)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSaveGetAndUpsertItemState(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	original := ItemState{
		ItemID:           7,
		MarketHashName:   "1st Lieutenant Farlow | SWAT",
		NameCN:           "第一中尉法洛 | 特警",
		Ratio:            core.RatioBP(6571),
		SteamDailyVolume: 344,
		SteamPrice:       "91.0000",
		Tier:             core.TierB2,
		NextCheckAt:      time.Date(2026, 8, 19, 23, 10, 0, 123000000, time.FixedZone("UTC+8", 8*60*60)),
	}
	if err := store.SaveItemState(ctx, original); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.GetItemState(ctx, original.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	expectedOriginal := original
	expectedOriginal.NextCheckAt = original.NextCheckAt.UTC()
	if !found || got != expectedOriginal {
		t.Fatalf("state = %#v, found=%t, want %#v", got, found, expectedOriginal)
	}
	if got.NextCheckAt.Location() != time.UTC {
		t.Fatalf("NextCheckAt location = %v, want UTC", got.NextCheckAt.Location())
	}
	var ratioType, nextCheckType string
	if err := store.db.QueryRow(
		"SELECT typeof(ratio_bp), typeof(next_check_at) FROM item_state WHERE item_id = ?",
		original.ItemID,
	).Scan(&ratioType, &nextCheckType); err != nil {
		t.Fatal(err)
	}
	if ratioType != "integer" || nextCheckType != "integer" {
		t.Fatalf("SQLite types ratio=%q next_check=%q, want integer/integer", ratioType, nextCheckType)
	}

	updated := ItemState{
		ItemID:           original.ItemID,
		MarketHashName:   "updated hash name",
		NameCN:           "更新名称",
		Ratio:            core.RatioBP(5987),
		SteamDailyVolume: 400,
		SteamPrice:       "",
		Tier:             core.TierA,
		NextCheckAt:      time.Date(2026, 8, 19, 15, 5, 0, 0, time.UTC),
	}
	if err := store.SaveItemState(ctx, updated); err != nil {
		t.Fatal(err)
	}
	got, found, err = store.GetItemState(ctx, updated.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || got != updated {
		t.Fatalf("updated state = %#v, found=%t, want %#v", got, found, updated)
	}
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM item_state WHERE item_id = ?", updated.ItemID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want 1", count)
	}
}

func TestGetItemStateNotFound(t *testing.T) {
	store := openTestStore(t)
	state, found, err := store.GetItemState(context.Background(), 999)
	if err != nil {
		t.Fatal(err)
	}
	if found || state != (ItemState{}) {
		t.Fatalf("state = %#v, found=%t, want zero state and false", state, found)
	}
}

func TestItemStatePersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	ctx := context.Background()
	want := ItemState{
		ItemID:           53,
		MarketHashName:   "AK-47 | Elite Build (Field-Tested)",
		NameCN:           "AK-47 | 精英之作 (久经沙场)",
		Ratio:            core.RatioBP(6000),
		SteamDailyVolume: 350,
		SteamPrice:       "17.73",
		Tier:             core.TierA,
		NextCheckAt:      time.Date(2026, 8, 19, 15, 5, 0, 0, time.UTC),
	}
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SaveItemState(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, found, err := second.GetItemState(ctx, want.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || got != want {
		t.Fatalf("restarted state = %#v, found=%t, want %#v", got, found, want)
	}
}

func TestInvalidStateDoesNotOverwriteValidState(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	valid := ItemState{
		ItemID:           53,
		MarketHashName:   "AK-47 | Elite Build (Field-Tested)",
		NameCN:           "AK-47 | 精英之作 (久经沙场)",
		Ratio:            core.RatioBP(6000),
		SteamDailyVolume: 350,
		SteamPrice:       "17.73",
		Tier:             core.TierA,
		NextCheckAt:      time.Date(2026, 8, 19, 15, 5, 0, 0, time.UTC),
	}
	if err := store.SaveItemState(ctx, valid); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		state ItemState
	}{
		{"non-positive item ID", ItemState{ItemID: 0, Ratio: 6000, SteamDailyVolume: 350, Tier: core.TierA, NextCheckAt: valid.NextCheckAt}},
		{"negative ratio", ItemState{ItemID: 53, Ratio: -1, SteamDailyVolume: 350, Tier: core.TierA, NextCheckAt: valid.NextCheckAt}},
		{"negative volume", ItemState{ItemID: 53, Ratio: 6000, SteamDailyVolume: -1, Tier: core.TierA, NextCheckAt: valid.NextCheckAt}},
		{"invalid tier", ItemState{ItemID: 53, Ratio: 6000, SteamDailyVolume: 350, Tier: core.Tier("UNKNOWN"), NextCheckAt: valid.NextCheckAt}},
		{"zero next check time", ItemState{ItemID: 53, Ratio: 6000, SteamDailyVolume: 350, Tier: core.TierA}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if err := store.SaveItemState(ctx, testCase.state); err == nil {
				t.Fatal("invalid state was accepted")
			}
			got, found, err := store.GetItemState(ctx, valid.ItemID)
			if err != nil {
				t.Fatal(err)
			}
			if !found || got != valid {
				t.Fatalf("valid state changed to %#v, found=%t", got, found)
			}
		})
	}
}

func TestListDueItemStates(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	states := []ItemState{
		testItemState(2, now),
		testItemState(4, now.Add(time.Millisecond)),
		testItemState(3, now.Add(-time.Minute)),
		testItemState(1, now),
	}
	for _, state := range states {
		if err := stateStore.SaveItemState(ctx, state); err != nil {
			t.Fatal(err)
		}
	}

	due, err := stateStore.ListDueItemStates(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []int64{3, 1, 2}
	if len(due) != len(wantIDs) {
		t.Fatalf("due count = %d, want %d", len(due), len(wantIDs))
	}
	for index, wantID := range wantIDs {
		if due[index].ItemID != wantID {
			t.Fatalf("due order at %d = %d, want %d", index, due[index].ItemID, wantID)
		}
	}
}

func testItemState(itemID int64, nextCheckAt time.Time) ItemState {
	return ItemState{
		ItemID:           itemID,
		MarketHashName:   "hash",
		NameCN:           "name",
		Ratio:            6000,
		SteamDailyVolume: 350,
		SteamPrice:       "17.73",
		Tier:             core.TierA,
		NextCheckAt:      nextCheckAt,
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}
