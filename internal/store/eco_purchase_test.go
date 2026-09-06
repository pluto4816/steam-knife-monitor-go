package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/c5"
	"github.com/pluto4816/steam-knife-monitor-go/internal/eco"
)

func TestECOReservationCountsC5DailyBudget(t *testing.T) {
	for _, existing := range []int{9, 10} {
		s, err := Open(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		at := time.Date(2026, 9, 6, 16, 1, 0, 0, time.UTC)
		for i := 0; i < existing; i++ {
			purchase, err := s.BeginC5Purchase(ctx, BeginC5PurchaseParams{ItemID: int64(i + 1), MarketHashName: "other", NameCN: "other", OutTradeNo: string(rune('a' + i)), QuotedPrice: c5.MoneyFen(100), SteamPrice: "1.00", BudgetDay: "2026-09-07", CreatedAt: at})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.MarkC5PurchasePlaced(ctx, purchase.ID, 100, "123", at); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.InitECOTest(ctx); err != nil {
			t.Fatal(err)
		}
		_, err = s.ReserveECOTest(ctx, "test", at)
		if (existing == 10) != (err != nil) {
			t.Fatalf("existing=%d err=%v", existing, err)
		}
		s.Close()
	}
}

func TestECOLedgerPreventsDuplicateAndOrderReplacement(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.InitECOTest(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.ReserveECOTest(ctx, "original", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveECOTest(ctx, "new-number", time.Now().Add(24*time.Hour)); err == nil {
		t.Fatal("reserved twice")
	}
	if err := s.MarkECOPlaced(ctx, p.ID, eco.BuyResult{OrderNo: "123", MerchantNo: "original", Paid: 36}); err != nil {
		t.Fatal(err)
	}
	for _, o := range []eco.Order{
		{OrderNo: "999", MerchantNo: "original", HashName: eco.TestHashName, Paid: 36},
		{OrderNo: "123", MerchantNo: "wrong", HashName: eco.TestHashName, Paid: 36},
		{OrderNo: "123", MerchantNo: "original", HashName: eco.TestHashName, Paid: 101},
	} {
		if err := s.SaveECOOrder(ctx, p.ID, o); err == nil {
			t.Fatal("accepted changed order", o)
		}
	}
}

func TestECOOldManualLedgerMigrationAndDryRunRead(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_, err = s.db.Exec(`CREATE TABLE eco_purchases (
 id INTEGER PRIMARY KEY,market_hash_name TEXT NOT NULL UNIQUE,merchant_no TEXT NOT NULL UNIQUE,
 budget_day TEXT NOT NULL,reserved_fen INTEGER NOT NULL,status TEXT NOT NULL,order_no TEXT NOT NULL DEFAULT '',
 paid_fen INTEGER,order_state INTEGER,purchase_kind TEXT NOT NULL DEFAULT 'MANUAL_TEST' CHECK(purchase_kind='MANUAL_TEST'),
 created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
 INSERT INTO eco_purchases VALUES(1,'P250 | Re.built (Field-Tested)','original','2026-09-06',100,'PLACED','123',45,1,'MANUAL_TEST',1000,1000)`)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='eco_purchases'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	allowed, err := s.CanAttemptECO(ctx, 42, eco.TestHashName, 45, time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	if err != nil || allowed {
		t.Fatal("old manual purchase not excluded", allowed, err)
	}
	var after string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='eco_purchases'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("read-only eligibility migrated schema")
	}
	if err := s.InitECOPurchases(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.InitECOPurchases(ctx); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	rows, err := s.ListECOPurchases(ctx)
	if err != nil || len(rows) != 1 || rows[0].MerchantNo != "original" || rows[0].Paid != 45 || rows[0].Reserved != 100 || rows[0].Kind != "MANUAL_TEST" || *rows[0].OrderState != 1 {
		t.Fatal(rows, err)
	}
	if _, err := s.ReserveECOPurchase(ctx, ECOReservation{ItemID: 42, HashName: eco.TestHashName, NameCN: "test", MerchantNo: "new", SteamPrice: "1", Ceiling: 50, Kind: "AUTO", At: time.Now()}); err == nil {
		t.Fatal("repurchased manual item")
	}
}

func TestECOAutomaticDailyLimitAndItemIdentity(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auto.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.InitECOPurchases(ctx); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 6, 16, 1, 0, 0, time.UTC)
	for i := 1; i <= 10; i++ {
		hash := fmt.Sprintf("item-%d", i)
		no := fmt.Sprintf("merchant-%d", i)
		p, err := s.ReserveECOPurchase(ctx, ECOReservation{ItemID: int64(i), HashName: hash, NameCN: hash, MerchantNo: no, SteamPrice: "2", Ceiling: 100, Kind: "AUTO", At: at})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SaveECOOrder(ctx, p.ID, eco.Order{OrderNo: "123", MerchantNo: no, HashName: hash, Paid: 45, State: 1}); err != nil {
			t.Fatal(err)
		}
	}
	allowed, err := s.CanAttemptECO(ctx, 11, "item-11", 1, at)
	if err != nil || allowed {
		t.Fatal("daily limit not enforced")
	}
	if _, err := s.ReserveECOPurchase(ctx, ECOReservation{ItemID: 11, HashName: "item-11", NameCN: "test", MerchantNo: "next", SteamPrice: "2", Ceiling: 1, Kind: "AUTO", At: at}); err == nil {
		t.Fatal("reserved over budget")
	}
	allowed, err = s.CanAttemptECO(ctx, 11, "item-11", 100, at.Add(24*time.Hour))
	if err != nil || !allowed {
		t.Fatal("next day budget not reset", err)
	}
	allowed, err = s.CanAttemptECO(ctx, 1, "changed-name", 1, at.Add(24*time.Hour))
	if err != nil || allowed {
		t.Fatal("numeric identity did not prevent repurchase")
	}
}
