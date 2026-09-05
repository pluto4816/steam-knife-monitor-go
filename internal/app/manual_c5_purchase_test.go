package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/c5"
	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

func TestManualC5PurchaseTestUsesOneYuanCeilingAndSubmitsOnce(t *testing.T) {
	stateStore := openTestStore(t)
	api := &fakeC5API{
		balance:     1000,
		quickResult: c5.QuickBuyResult{ActualPay: 88, OrderID: "manual-order"},
	}
	service, err := NewManualC5PurchaseTestService(
		stateStore,
		api,
		catalog.Item{ItemID: 79171, MarketHashName: "Five-SeveN | Dark Polymer (Field-Tested)", NameCN: "FN57 | 暗夜聚合 (久经沙场)"},
		C5PerOrderLimit,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }
	service.newTradeNo = func(time.Time) (string, error) { return "manual-trade", nil }

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfirmedCount != 1 || result.PlacedCount != 1 || result.PlacedPurchase == nil {
		t.Fatalf("result = %#v", result)
	}
	if got := *result.PlacedPurchase; got != (C5PurchaseReceipt{
		NameCN: "FN57 | 暗夜聚合 (久经沙场)", PurchasePrice: 88, ManualTest: true,
	}) {
		t.Fatalf("receipt = %#v", got)
	}
	if api.balanceCalls != 1 || len(api.quickRequests) != 1 {
		t.Fatalf("C5 calls: balance=%d quick=%#v", api.balanceCalls, api.quickRequests)
	}
	request := api.quickRequests[0]
	if request.OutTradeNo != "manual-trade" || request.MaxPrice != "1.00" ||
		request.MarketHashName != "Five-SeveN | Dark Polymer (Field-Tested)" {
		t.Fatalf("quick request = %#v", request)
	}

	if _, err := service.Run(context.Background()); !errors.Is(err, store.ErrC5ItemHandled) {
		t.Fatalf("second run error = %v, want ErrC5ItemHandled", err)
	}
	if api.balanceCalls != 1 || len(api.quickRequests) != 1 {
		t.Fatalf("manual purchase was resubmitted: balance=%d quick=%d", api.balanceCalls, len(api.quickRequests))
	}
}

func TestManualC5PurchaseTestFailureBecomesUncertainWithoutResubmission(t *testing.T) {
	stateStore := openTestStore(t)
	api := &fakeC5API{
		balance:  1000,
		quickErr: errors.New("ambiguous timeout"),
	}
	service, err := NewManualC5PurchaseTestService(
		stateStore,
		api,
		catalog.Item{ItemID: 7, MarketHashName: "target-hash", NameCN: "target"},
		C5PerOrderLimit,
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC) }
	service.newTradeNo = func(time.Time) (string, error) { return "manual-uncertain", nil }

	if _, err := service.Run(context.Background()); err == nil {
		t.Fatal("ambiguous manual quick-buy unexpectedly succeeded")
	}
	unresolved, err := stateStore.ListUnresolvedC5Purchases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 1 || unresolved[0].Status != store.C5PurchaseUncertain ||
		unresolved[0].Kind != store.C5PurchaseManualTest || unresolved[0].QuotedPrice != 100 {
		t.Fatalf("unresolved = %#v", unresolved)
	}

	api.orderErr = errors.New("not found")
	if _, err := service.Run(context.Background()); err == nil {
		t.Fatal("unresolved reconciliation unexpectedly succeeded")
	}
	if len(api.quickRequests) != 1 || len(api.orderTradeNos) != 1 ||
		api.orderTradeNos[0] != "manual-uncertain" {
		t.Fatalf("calls after failure: quick=%#v order=%#v", api.quickRequests, api.orderTradeNos)
	}
}

func TestManualC5PurchaseTestRejectsCeilingAboveOneYuan(t *testing.T) {
	_, err := NewManualC5PurchaseTestService(
		openTestStore(t),
		&fakeC5API{},
		catalog.Item{ItemID: 1, MarketHashName: "hash", NameCN: "name"},
		C5PerOrderLimit+1,
	)
	if err == nil {
		t.Fatal("manual C5 purchase ceiling above one yuan was accepted")
	}
}
