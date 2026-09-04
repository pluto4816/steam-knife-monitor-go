package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/c5"
	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

func TestC5PurchaseDryRunConfirmsWithoutC5OrLedgerMutation(t *testing.T) {
	stateStore := openTestStore(t)
	saveC5AState(t, stateStore, 1)
	client, requests := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		if path == "/api/commodity/1" {
			return http.StatusOK, c5DetailData(1, "2.30", "0.99", 400)
		}
		return http.StatusInternalServerError, "{}"
	})
	service, err := NewC5PurchaseService(
		C5PurchaseDryRun,
		client,
		&fakeSteamPriceSource{prices: map[string]string{"hash-1": "2.30"}},
		stateStore,
		nil,
		c5Candidates(1),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result != (PurchaseRunResult{ConfirmedCount: 1, DryRunCount: 1}) {
		t.Fatalf("result = %#v", result)
	}
	if requests.countPath("/api/commodity/1") != 1 {
		t.Fatalf("requests = %#v", requests.snapshot())
	}
	unresolved, err := stateStore.ListUnresolvedC5Purchases(context.Background())
	if err != nil || len(unresolved) != 0 {
		t.Fatalf("dry-run ledger = %#v, %v", unresolved, err)
	}
	canAttempt, err := stateStore.CanAttemptC5Purchase(context.Background(), 1, "2026-09-02")
	if err != nil || !canAttempt {
		t.Fatalf("dry-run mutated eligibility: %t, %v", canAttempt, err)
	}
}

func TestC5PurchaseLiveUsesExactRefreshedCeilingAndBuysOnce(t *testing.T) {
	stateStore := openTestStore(t)
	saveC5AState(t, stateStore, 1)
	client, requests := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		if path == "/api/commodity/1" {
			return http.StatusOK, c5DetailData(1, "2.30", "0.9900", 400)
		}
		return http.StatusInternalServerError, "{}"
	})
	api := &fakeC5API{
		balance:     1000,
		quickResult: c5.QuickBuyResult{ActualPay: 98, OrderID: "42"},
	}
	service, err := NewC5PurchaseService(
		C5PurchaseLive,
		client,
		&fakeSteamPriceSource{prices: map[string]string{"hash-1": "2.30"}},
		stateStore,
		api,
		c5Candidates(1),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Date(2026, 9, 1, 16, 30, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }
	service.newTradeNo = func(time.Time) (string, error) { return "durable-1", nil }

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfirmedCount != 1 || result.PlacedCount != 1 || result.PlacedPurchase == nil {
		t.Fatalf("result = %#v", result)
	}
	if got := *result.PlacedPurchase; got != (C5PurchaseReceipt{
		NameCN: "test", PurchasePrice: 98, SteamPrice: "2.30", Ratio: 4900,
	}) {
		t.Fatalf("placed purchase = %#v", got)
	}
	if api.balanceCalls != 1 || len(api.quickRequests) != 1 {
		t.Fatalf("C5 calls: balance=%d quick=%#v", api.balanceCalls, api.quickRequests)
	}
	request := api.quickRequests[0]
	if request.OutTradeNo != "durable-1" || request.MaxPrice != "0.9900" ||
		request.MarketHashName != "hash-1" {
		t.Fatalf("quick request = %#v", request)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(api.quickRequests) != 1 || requests.countPath("/api/commodity/1") != 1 {
		t.Fatalf("item was reconsidered after purchase: quick=%d smis=%d", len(api.quickRequests), requests.countPath("/api/commodity/1"))
	}
}

func TestC5PurchaseApplicationFailureIsUncertainAndReconcilesOriginalTradeNumber(t *testing.T) {
	stateStore := openTestStore(t)
	saveC5AState(t, stateStore, 1)
	client, _ := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		if path == "/api/commodity/1" {
			return http.StatusOK, c5DetailData(1, "2.30", "0.57", 400)
		}
		return http.StatusInternalServerError, "{}"
	})
	api := &fakeC5API{
		balance:  1000,
		quickErr: &c5.APIError{Code: "NO_MONEY", Message: "insufficient"},
	}
	service, err := NewC5PurchaseService(
		C5PurchaseLive,
		client,
		&fakeSteamPriceSource{prices: map[string]string{"hash-1": "2.30"}},
		stateStore,
		api,
		c5Candidates(1),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }
	service.newTradeNo = func(time.Time) (string, error) { return "original-trade", nil }

	if _, err := service.Run(context.Background()); err == nil {
		t.Fatal("ambiguous quick-buy unexpectedly succeeded")
	}
	unresolved, err := stateStore.ListUnresolvedC5Purchases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 1 || unresolved[0].Status != store.C5PurchaseUncertain ||
		unresolved[0].OutTradeNo != "original-trade" {
		t.Fatalf("unresolved = %#v", unresolved)
	}

	api.orderErr = errors.New("not found yet")
	if _, err := service.Run(context.Background()); err == nil {
		t.Fatal("unresolved reconciliation unexpectedly succeeded")
	}
	if len(api.quickRequests) != 1 || len(api.orderTradeNos) != 1 ||
		api.orderTradeNos[0] != "original-trade" {
		t.Fatalf("calls after failed reconciliation: quick=%#v order=%#v", api.quickRequests, api.orderTradeNos)
	}

	api.orderErr = nil
	api.orderResult = c5.Order{ActualPay: 57, OrderID: "99"}
	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ReconciledCount != 1 || result.PlacedPurchase == nil || len(api.quickRequests) != 1 {
		t.Fatalf("reconciliation result=%#v quick=%d", result, len(api.quickRequests))
	}
	if got := *result.PlacedPurchase; got != (C5PurchaseReceipt{
		NameCN: "test", PurchasePrice: 57, SteamPrice: "2.30", Ratio: 2850,
	}) {
		t.Fatalf("reconciled purchase = %#v", got)
	}
	if rows, err := stateStore.ListUnresolvedC5Purchases(context.Background()); err != nil || len(rows) != 0 {
		t.Fatalf("unresolved after reconciliation = %#v, %v", rows, err)
	}
}

func TestC5PurchaseRecalculatesBaseAWithOfficialSteamPrice(t *testing.T) {
	stateStore := openTestStore(t)
	saveC5AState(t, stateStore, 1)
	client, _ := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		if path == "/api/commodity/1" {
			return http.StatusOK, c5DetailData(1, "2.30", "0.99", 400)
		}
		return http.StatusInternalServerError, "{}"
	})
	steam := &fakeSteamPriceSource{prices: map[string]string{"hash-1": "1.15"}}
	service, err := NewC5PurchaseService(
		C5PurchaseDryRun,
		client,
		steam,
		stateStore,
		nil,
		c5Candidates(1),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result != (PurchaseRunResult{ExcludedCount: 1}) {
		t.Fatalf("result = %#v", result)
	}
	if len(steam.calls) != 1 || steam.calls[0] != "hash-1" {
		t.Fatalf("Steam calls = %#v", steam.calls)
	}
	if rows, err := stateStore.ListUnresolvedC5Purchases(context.Background()); err != nil || len(rows) != 0 {
		t.Fatalf("purchase ledger = %#v, %v", rows, err)
	}
}

func TestC5PurchaseStopsWhenOfficialSteamPriceIsUnavailable(t *testing.T) {
	stateStore := openTestStore(t)
	saveC5AState(t, stateStore, 1)
	client, _ := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		if path == "/api/commodity/1" {
			return http.StatusOK, c5DetailData(1, "2.30", "0.99", 400)
		}
		return http.StatusInternalServerError, "{}"
	})
	steam := &fakeSteamPriceSource{err: errors.New("Steam unavailable")}
	service, err := NewC5PurchaseService(
		C5PurchaseDryRun,
		client,
		steam,
		stateStore,
		nil,
		c5Candidates(1),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "confirm Steam price") {
		t.Fatalf("error = %v", err)
	}
	if result != (PurchaseRunResult{ExcludedCount: 1}) {
		t.Fatalf("result = %#v", result)
	}
	if rows, listErr := stateStore.ListUnresolvedC5Purchases(context.Background()); listErr != nil || len(rows) != 0 {
		t.Fatalf("purchase ledger = %#v, %v", rows, listErr)
	}
}

func TestC5PurchaseDoesNotQuerySteamWhenC5IsNotLowest(t *testing.T) {
	stateStore := openTestStore(t)
	saveC5AState(t, stateStore, 1)
	client, _ := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		if path == "/api/commodity/1" {
			return http.StatusOK, `{"id":1,"hashName":"hash-1","cnName":"test",` +
				`"steamSellPrice":2.30,"steamTransactionQuantity":400,` +
				`"buffSellPrice":0.50,"uuypSellPrice":2,"c5SellPrice":0.99,` +
				`"igxeSellPrice":2,"ecoSellPrice":2,"updateTime":"now"}`
		}
		return http.StatusInternalServerError, "{}"
	})
	steamPrices := &fakeSteamPriceSource{err: errors.New("must not be called")}
	service, err := NewC5PurchaseService(
		C5PurchaseDryRun, client, steamPrices, stateStore, nil, c5Candidates(1), 0,
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result != (PurchaseRunResult{ExcludedCount: 1}) {
		t.Fatalf("result = %#v", result)
	}
	if len(steamPrices.calls) != 0 {
		t.Fatalf("Steam calls = %#v", steamPrices.calls)
	}
}

func TestC5PurchaseRequiresBaseAAndOneYuanLimit(t *testing.T) {
	tests := []struct {
		name           string
		price          string
		volume         int
		steam          string
		wantSteamCalls int
	}{
		{name: "hysteresis-only A", steam: "1.15", price: "0.61", volume: 330, wantSteamCalls: 1},
		{name: "above one yuan", steam: "2.30", price: "1.01", volume: 400, wantSteamCalls: 0},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			stateStore := openTestStore(t)
			saveC5AState(t, stateStore, 1)
			client, _ := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
				if path == "/api/commodity/1" {
					return http.StatusOK, c5DetailData(1, testCase.steam, testCase.price, testCase.volume)
				}
				return http.StatusInternalServerError, "{}"
			})
			api := &fakeC5API{balance: 1000}
			steamPrices := &fakeSteamPriceSource{prices: map[string]string{"hash-1": testCase.steam}}
			service, err := NewC5PurchaseService(
				C5PurchaseLive,
				client,
				steamPrices,
				stateStore,
				api,
				c5Candidates(1),
				0,
			)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.ConfirmedCount != 0 || api.balanceCalls != 0 || len(api.quickRequests) != 0 {
				t.Fatalf("unsafe candidate reached C5: result=%#v api=%#v", result, api)
			}
			if len(steamPrices.calls) != testCase.wantSteamCalls {
				t.Fatalf("Steam calls = %#v, want %d", steamPrices.calls, testCase.wantSteamCalls)
			}
		})
	}
}

func TestC5PurchaseLiveStopsAfterFirstPlacedItem(t *testing.T) {
	stateStore := openTestStore(t)
	saveC5AState(t, stateStore, 1)
	saveC5AState(t, stateStore, 2)
	client, requests := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		switch path {
		case "/api/commodity/1":
			return http.StatusOK, c5DetailData(1, "2.30", "0.99", 400)
		case "/api/commodity/2":
			return http.StatusOK, c5DetailData(2, "2.30", "0.98", 400)
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	api := &fakeC5API{
		balance:     1000,
		quickResult: c5.QuickBuyResult{ActualPay: 98, OrderID: "42"},
	}
	steam := &fakeSteamPriceSource{prices: map[string]string{
		"hash-1": "2.30",
		"hash-2": "2.30",
	}}
	service, err := NewC5PurchaseService(
		C5PurchaseLive, client, steam, stateStore, api, c5Candidates(1, 2), 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	service.newTradeNo = func(time.Time) (string, error) { return "first-only", nil }

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.PlacedCount != 1 || result.PlacedPurchase == nil {
		t.Fatalf("result = %#v", result)
	}
	if len(api.quickRequests) != 1 || requests.countPath("/api/commodity/2") != 0 {
		t.Fatalf("continued after first purchase: quick=%d item2=%d", len(api.quickRequests), requests.countPath("/api/commodity/2"))
	}
	if len(steam.calls) != 1 || steam.calls[0] != "hash-1" {
		t.Fatalf("Steam calls = %#v, want only first item", steam.calls)
	}
}

type fakeC5API struct {
	balance       c5.MoneyFen
	balanceCalls  int
	quickRequests []c5.QuickBuyRequest
	quickResult   c5.QuickBuyResult
	quickErr      error
	orderTradeNos []string
	orderResult   c5.Order
	orderErr      error
}

func (api *fakeC5API) Balance(context.Context) (c5.MoneyFen, error) {
	api.balanceCalls++
	return api.balance, nil
}

func (api *fakeC5API) QuickBuy(_ context.Context, request c5.QuickBuyRequest) (c5.QuickBuyResult, error) {
	api.quickRequests = append(api.quickRequests, request)
	return api.quickResult, api.quickErr
}

func (api *fakeC5API) OrderByOutTradeNo(_ context.Context, tradeNo string) (c5.Order, error) {
	api.orderTradeNos = append(api.orderTradeNos, tradeNo)
	return api.orderResult, api.orderErr
}

func saveC5AState(t *testing.T, stateStore *store.Store, itemID int64) {
	t.Helper()
	saveState(t, stateStore, store.ItemState{
		ItemID: itemID, MarketHashName: fmt.Sprintf("hash-%d", itemID),
		NameCN: "test", Ratio: core.RatioBP(5500), SteamDailyVolume: 400,
		SteamPrice: "2.00", Tier: core.TierA, NextCheckAt: time.Now().UTC().Add(time.Hour),
	})
}

func c5Candidates(itemIDs ...int64) []catalog.Item {
	items := make([]catalog.Item, 0, len(itemIDs))
	for _, itemID := range itemIDs {
		items = append(items, catalog.Item{
			ItemID: itemID, MarketHashName: fmt.Sprintf("hash-%d", itemID), NameCN: "test",
		})
	}
	return items
}

func c5DetailData(itemID int64, steamPrice, c5Price string, volume int) string {
	return fmt.Sprintf(
		`{"id":%d,"hashName":"hash-%d","cnName":"test","steamSellPrice":%s,`+
			`"steamTransactionQuantity":%d,"buffSellPrice":2,"uuypSellPrice":2,`+
			`"c5SellPrice":%s,"igxeSellPrice":2,"ecoSellPrice":2,"updateTime":"now"}`,
		itemID, itemID, steamPrice, volume, c5Price,
	)
}
func TestRunOneShotFailedDueChecksDoNotPurchase(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, dueState(53, time.Now().UTC().Add(-time.Minute)))
	client, _ := runtimeTestClient(t, func(method, path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, "{\"items\":[]}"
		case method == http.MethodGet && path == "/api/commodity/53":
			return http.StatusInternalServerError, "{}"
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	purchase := &countingPurchaseRunner{}
	config := runtimeTestConfig()
	config.Purchase = purchase

	if _, err := RunOneShot(context.Background(), client, stateStore, config, nil); err == nil {
		t.Fatal("failed due checks unexpectedly succeeded")
	}
	if purchase.calls != 0 {
		t.Fatalf("purchase calls = %d, want none after failed due checks", purchase.calls)
	}
}

func TestRunOneShotPurchasesBeforeQQDelivery(t *testing.T) {
	stateStore := openTestStore(t)
	client, _ := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		if path == "/api/commodity/1" {
			return http.StatusOK, detailData(1, "0.59", 400, "2026-09-02 01:00:00")
		}
		return http.StatusInternalServerError, "{}"
	})
	var events []string
	purchase := &countingPurchaseRunner{onRun: func() {
		events = append(events, "purchase")
	}}
	sender := callbackSender(func(context.Context, store.Alert) error {
		events = append(events, "notify")
		return nil
	})
	config := runtimeTestConfig()
	config.Candidates = c5Candidates(1)
	config.Purchase = purchase

	if _, err := RunOneShot(context.Background(), client, stateStore, config, sender); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(events) != "[purchase notify]" {
		t.Fatalf("phase order = %v, want purchase before notify", events)
	}
}

type countingPurchaseRunner struct {
	calls  int
	result PurchaseRunResult
	onRun  func()
	err    error
}

func (runner *countingPurchaseRunner) Run(context.Context) (PurchaseRunResult, error) {
	runner.calls++
	if runner.onRun != nil {
		runner.onRun()
	}
	return runner.result, runner.err
}
