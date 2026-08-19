package store

import (
	"context"
	"path/filepath"
	"testing"

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
	}
	if err := store.SaveItemState(ctx, original); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.GetItemState(ctx, original.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || got != original {
		t.Fatalf("state = %#v, found=%t, want %#v", got, found, original)
	}
	var ratioType string
	if err := store.db.QueryRow("SELECT typeof(ratio_bp) FROM item_state WHERE item_id = ?", original.ItemID).Scan(&ratioType); err != nil {
		t.Fatal(err)
	}
	if ratioType != "integer" {
		t.Fatalf("ratio_bp SQLite type = %q, want integer", ratioType)
	}

	updated := ItemState{
		ItemID:           original.ItemID,
		MarketHashName:   "updated hash name",
		NameCN:           "更新名称",
		Ratio:            core.RatioBP(5987),
		SteamDailyVolume: 400,
		SteamPrice:       "",
		Tier:             core.TierA,
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
	}
	if err := store.SaveItemState(ctx, valid); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		state ItemState
	}{
		{"non-positive item ID", ItemState{ItemID: 0, Ratio: 6000, SteamDailyVolume: 350, Tier: core.TierA}},
		{"negative ratio", ItemState{ItemID: 53, Ratio: -1, SteamDailyVolume: 350, Tier: core.TierA}},
		{"negative volume", ItemState{ItemID: 53, Ratio: 6000, SteamDailyVolume: -1, Tier: core.TierA}},
		{"invalid tier", ItemState{ItemID: 53, Ratio: 6000, SteamDailyVolume: 350, Tier: core.Tier("UNKNOWN")}},
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
