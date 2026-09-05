package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/c5"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

func TestC5PurchaseDailyBudgetIncludesPlacedAndUnresolved(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)

	for itemID := int64(1); itemID <= 10; itemID++ {
		purchase, err := stateStore.BeginC5Purchase(ctx, c5PurchaseParams(itemID, now))
		if err != nil {
			t.Fatalf("reserve item %d: %v", itemID, err)
		}
		if itemID <= 9 {
			if err := stateStore.MarkC5PurchasePlaced(ctx, purchase.ID, 100, fmt.Sprint(itemID), now); err != nil {
				t.Fatalf("place item %d: %v", itemID, err)
			}
		} else if err := stateStore.MarkC5PurchaseUncertain(ctx, purchase.ID, "timeout", now); err != nil {
			t.Fatal(err)
		}
	}
	params := c5PurchaseParams(11, now)
	params.QuotedPrice = 1
	if _, err := stateStore.BeginC5Purchase(ctx, params); !errors.Is(err, ErrC5DailyLimit) {
		t.Fatalf("budget overflow error = %v, want ErrC5DailyLimit", err)
	}
	unresolved, err := stateStore.ListUnresolvedC5Purchases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 1 || unresolved[0].ItemID != 10 ||
		unresolved[0].Status != C5PurchaseUncertain ||
		unresolved[0].QuotedPrice != 100 {
		t.Fatalf("unresolved = %#v", unresolved)
	}
}

func TestC5PurchasePerItemAndSameDayRules(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)

	rejected, err := stateStore.BeginC5Purchase(ctx, c5PurchaseParams(1, now))
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkC5PurchaseRejected(ctx, rejected.ID, "sold", now); err != nil {
		t.Fatal(err)
	}
	canAttempt, err := stateStore.CanAttemptC5Purchase(ctx, 1, "2026-09-02")
	if err != nil || canAttempt {
		t.Fatalf("same-day rejected eligibility = %t, %v; want false", canAttempt, err)
	}
	canAttempt, err = stateStore.CanAttemptC5Purchase(ctx, 1, "2026-09-03")
	if err != nil || !canAttempt {
		t.Fatalf("next-day rejected eligibility = %t, %v; want true", canAttempt, err)
	}

	nextDay := c5PurchaseParams(1, now.Add(24*time.Hour))
	placed, err := stateStore.BeginC5Purchase(ctx, nextDay)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkC5PurchasePlaced(ctx, placed.ID, 90, "order-1", now); err != nil {
		t.Fatal(err)
	}
	canAttempt, err = stateStore.CanAttemptC5Purchase(ctx, 1, "2026-09-04")
	if err != nil || canAttempt {
		t.Fatalf("placed item eligibility = %t, %v; want false", canAttempt, err)
	}
	if _, err := stateStore.BeginC5Purchase(ctx, c5PurchaseParams(1, now.Add(48*time.Hour))); !errors.Is(err, ErrC5ItemHandled) {
		t.Fatalf("duplicate placed item error = %v, want ErrC5ItemHandled", err)
	}
}

func TestC5PurchaseCannotPlaceAboveReservedQuote(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	overLimit := c5PurchaseParams(8, now)
	overLimit.QuotedPrice = 101
	if _, err := stateStore.BeginC5Purchase(ctx, overLimit); err == nil {
		t.Fatal("reserved purchase above one yuan")
	}

	purchase, err := stateStore.BeginC5Purchase(ctx, c5PurchaseParams(7, now))
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkC5PurchasePlaced(ctx, purchase.ID, 101, "order", now); err == nil {
		t.Fatal("placed purchase above quote")
	}
	unresolved, err := stateStore.ListUnresolvedC5Purchases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 1 || unresolved[0].Status != C5PurchaseSubmitting {
		t.Fatalf("purchase after rejected transition = %#v", unresolved)
	}
}

func TestC5PurchaseKindMigratesExistingLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old-ledger.db")
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
CREATE TABLE c5_purchases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id INTEGER NOT NULL,
    market_hash_name TEXT NOT NULL,
    name_cn TEXT NOT NULL,
    out_trade_no TEXT NOT NULL UNIQUE,
    quoted_price_fen INTEGER NOT NULL,
    steam_price TEXT NOT NULL,
    ratio_bp INTEGER NOT NULL,
    budget_day TEXT NOT NULL,
    status TEXT NOT NULL,
    actual_price_fen INTEGER,
    c5_order_id TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (item_id, budget_day)
)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	stateStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	now := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	params := c5PurchaseParams(77, now)
	params.Kind = C5PurchaseManualTest
	if _, err := stateStore.BeginC5Purchase(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	rows, err := stateStore.ListUnresolvedC5Purchases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Kind != C5PurchaseManualTest {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestListItemStatesByTier(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	for itemID, tier := range map[int64]core.Tier{1: core.TierA, 2: core.TierB1, 3: core.TierA} {
		if err := stateStore.SaveItemState(ctx, ItemState{
			ItemID: itemID, MarketHashName: fmt.Sprint("item-", itemID),
			NameCN: "name", Ratio: 5900, SteamDailyVolume: 400,
			SteamPrice: "2.00", Tier: tier, NextCheckAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	states, err := stateStore.ListItemStatesByTier(ctx, core.TierA)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 || states[0].ItemID != 1 || states[1].ItemID != 3 {
		t.Fatalf("A states = %#v", states)
	}
}

func c5PurchaseParams(itemID int64, at time.Time) BeginC5PurchaseParams {
	return BeginC5PurchaseParams{
		ItemID:         itemID,
		MarketHashName: fmt.Sprint("item-", itemID),
		NameCN:         "test",
		OutTradeNo:     fmt.Sprintf("trade-%d-%d", itemID, at.Unix()),
		QuotedPrice:    c5.MoneyFen(100),
		SteamPrice:     "2.00",
		Ratio:          core.RatioBP(5500),
		BudgetDay:      at.In(time.FixedZone("UTC+8", 8*60*60)).Format("2006-01-02"),
		CreatedAt:      at,
	}
}
