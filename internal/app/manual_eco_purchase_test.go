package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/eco"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type fakeECO struct {
	s                      *store.Store
	t                      *testing.T
	balance                eco.MoneyFen
	buys, balances, orders int
	buyErr, orderErr       error
	merchant               string
	cancel                 context.CancelFunc
}

func (f *fakeECO) Balance(context.Context) (eco.MoneyFen, error) { f.balances++; return f.balance, nil }
func (f *fakeECO) BuyTestItem(ctx context.Context, no string) (eco.BuyResult, error) {
	f.buys++
	f.merchant = no
	rows, err := f.s.ListECOPurchases(ctx)
	if err != nil || len(rows) != 1 || rows[0].Status != "SUBMITTING" || rows[0].MerchantNo != no {
		f.t.Fatal("payment called before durable reservation", rows, err)
	}
	if f.cancel != nil {
		f.cancel()
	}
	return eco.BuyResult{OrderNo: "123", MerchantNo: no, Paid: 36}, f.buyErr
}
func (f *fakeECO) Order(_ context.Context, no string) (eco.Order, error) {
	f.orders++
	if f.merchant != "" && f.merchant != no {
		f.t.Fatal("reconciliation changed merchant number")
	}
	return eco.Order{OrderNo: "123", MerchantNo: no, HashName: eco.TestHashName, Paid: 36, State: 1}, f.orderErr
}

func TestECOSinglePurchaseThenQueryOnlyAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeECO{s: s, t: t, balance: 100}
	result, err := RunManualECOTest(context.Background(), s, f)
	if err != nil || !result.Submitted || f.buys != 1 {
		t.Fatal(result, err)
	}
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f.s = s
	result, err = RunManualECOTest(context.Background(), s, f)
	if err != nil || !result.Reconciled || result.Submitted || f.buys != 1 || f.balances != 1 {
		t.Fatal(result, err, f)
	}
}

func TestECOAmbiguityAndCancellationNeverResubmit(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "transport", true: "cancelled"}[cancelled], func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &fakeECO{s: s, t: t, balance: 100, buyErr: errors.New("transport failed"), orderErr: errors.New("not found")}
			if cancelled {
				f.cancel = cancel
			}
			if _, err := RunManualECOTest(ctx, s, f); err == nil {
				t.Fatal("expected uncertainty")
			}
			rows, err := s.ListECOPurchases(context.Background())
			if err != nil || len(rows) != 1 || rows[0].Status != "UNCERTAIN" {
				t.Fatal(rows, err)
			}
			if _, err := RunManualECOTest(context.Background(), s, f); err == nil {
				t.Fatal("not-found must remain blocked")
			}
			if f.buys != 1 || f.balances != 1 || f.orders != 1 {
				t.Fatal(f)
			}
		})
	}
}

func TestECOInsufficientBalanceNoReservation(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := &fakeECO{s: s, t: t, balance: 99}
	if _, err := RunManualECOTest(context.Background(), s, f); err == nil {
		t.Fatal("accepted low balance")
	}
	rows, err := s.ListECOPurchases(context.Background())
	if err != nil || len(rows) != 0 || f.buys != 0 {
		t.Fatal(rows, err)
	}
}

func TestECOSubmittingCrashRecoveryQueriesOnly(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.InitECOTest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveECOTest(context.Background(), "original", time.Now()); err != nil {
		t.Fatal(err)
	}
	f := &fakeECO{s: s, t: t, merchant: "original"}
	result, err := RunManualECOTest(context.Background(), s, f)
	if err != nil || !result.Reconciled || f.buys != 0 || f.balances != 0 {
		t.Fatal(result, err)
	}
}
