package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/eco"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type fakeECOAuto struct {
	t                              *testing.T
	s                              *store.Store
	events                         *[]string
	quote, balance, paid           eco.MoneyFen
	quoteErr, buyErr, orderErr     error
	merchant, hash                 string
	ceiling                        eco.MoneyFen
	buys, quotes, balances, orders int
}

func (f *fakeECOAuto) Quote(context.Context, string) (eco.MoneyFen, error) {
	f.quotes++
	*f.events = append(*f.events, "eco_quote")
	return f.quote, f.quoteErr
}
func (f *fakeECOAuto) Balance(context.Context) (eco.MoneyFen, error) {
	f.balances++
	*f.events = append(*f.events, "balance")
	return f.balance, nil
}
func (f *fakeECOAuto) Buy(ctx context.Context, hash, no string, ceiling eco.MoneyFen) (eco.BuyResult, error) {
	f.buys++
	f.hash = hash
	f.merchant = no
	f.ceiling = ceiling
	rows, err := f.s.ListECOPurchases(ctx)
	if err != nil || len(rows) == 0 {
		f.t.Fatal("missing durable reservation", err)
	}
	p := rows[len(rows)-1]
	if p.Status != "SUBMITTING" || p.HashName != hash || p.MerchantNo != no || p.Reserved != ceiling || p.Kind != "AUTO" || p.SteamPrice == "" {
		f.t.Fatal("incorrect durable evidence", p)
	}
	*f.events = append(*f.events, "buy")
	return eco.BuyResult{OrderNo: "123", MerchantNo: no, Paid: f.paid}, f.buyErr
}
func (f *fakeECOAuto) OrderForItem(_ context.Context, no, hash string, ceiling eco.MoneyFen) (eco.Order, error) {
	f.orders++
	*f.events = append(*f.events, "order")
	if f.merchant != "" && (f.merchant != no || f.hash != hash || f.ceiling != ceiling) {
		f.t.Fatal("reconciliation identity changed")
	}
	return eco.Order{OrderNo: "123", MerchantNo: no, HashName: hash, Paid: f.paid, State: 1}, f.orderErr
}

type orderedSteam struct {
	events *[]string
	price  string
	calls  int
	err    error
}

func (s *orderedSteam) FetchLowestPrice(context.Context, string) (string, error) {
	s.calls++
	*s.events = append(*s.events, "steam")
	return s.price, s.err
}

func ecoDetail(id int64, price string, volume int) string {
	return strings.Replace(strings.Replace(c5DetailData(id, "2.30", price, volume), `"c5SellPrice":`+price, `"c5SellPrice":2`, 1), `"ecoSellPrice":2`, `"ecoSellPrice":`+price, 1)
}

func TestECOAutomaticOrderSequenceAndRepeatDedup(t *testing.T) {
	s := openTestStore(t)
	saveC5AState(t, s, 1)
	events := []string{}
	client, _ := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		events = append(events, "smis")
		return 200, ecoDetail(1, "0.55", 400)
	})
	steam := &orderedSteam{events: &events, price: "1.00"}
	api := &fakeECOAuto{t: t, s: s, events: &events, quote: 50, balance: 1000, paid: 45}
	runner, err := NewECOPurchaseService(ECOPurchaseLive, client, steam, s, api, c5Candidates(1), 0)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background())
	if err != nil || result.PlacedCount != 1 || result.ECOPlacedPurchase == nil || result.ECOPlacedPurchase.Price != 45 || api.ceiling != 50 {
		t.Fatal(result, err)
	}
	if !reflect.DeepEqual(events, []string{"smis", "eco_quote", "steam", "balance", "buy", "order"}) {
		t.Fatal(events)
	}
	count := len(events)
	result, err = runner.Run(context.Background())
	if err != nil || result.PlacedCount != 0 || len(events) != count || api.buys != 1 {
		t.Fatal("repurchased", result, err, events)
	}
}

func TestECOAutomaticPrefiltersPreventSteamAndPayment(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		quote      eco.MoneyFen
		quoteErr   error
		wantQuotes int
	}{
		{name: "wrong_platform", body: c5DetailData(1, "2.30", "0.50", 400)},
		{name: "over_limit", body: ecoDetail(1, "1.01", 400)},
		{name: "increased", body: ecoDetail(1, "0.50", 400), quote: 51, wantQuotes: 1},
		{name: "unavailable", body: ecoDetail(1, "0.50", 400), quote: 0, wantQuotes: 1},
		{name: "restricted", body: ecoDetail(1, "0.50", 400), quoteErr: errors.New("restricted"), wantQuotes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			saveC5AState(t, s, 1)
			events := []string{}
			client, _ := runtimeTestClient(t, func(_ string, _ string, _ int) (int, string) { return 200, tc.body })
			steam := &orderedSteam{events: &events, price: "2.30"}
			api := &fakeECOAuto{t: t, s: s, events: &events, quote: tc.quote, quoteErr: tc.quoteErr}
			r, err := NewECOPurchaseService(ECOPurchaseLive, client, steam, s, api, c5Candidates(1), 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = r.Run(context.Background())
			if steam.calls != 0 || api.buys != 0 || api.balances != 0 || api.quotes != tc.wantQuotes {
				t.Fatal(events)
			}
		})
	}
}

func TestECOAutomaticSteamUsesBaseAAndSMISVolume(t *testing.T) {
	for _, tc := range []struct {
		price    string
		volume   int
		steamErr error
	}{
		{"0.52", 400, nil}, // 0.52 / Steam net 0.86 = 0.6047: retained A, but fails base A
		{"0.50", 349, nil},
		{"0.50", 400, errors.New("missing Steam CNY price")},
	} {
		s := openTestStore(t)
		saveC5AState(t, s, 1)
		events := []string{}
		client, _ := runtimeTestClient(t, func(_ string, _ string, _ int) (int, string) { return 200, ecoDetail(1, tc.price, tc.volume) })
		q, _ := eco.ParseMoney(tc.price)
		steam := &orderedSteam{events: &events, price: "1.00", err: tc.steamErr}
		api := &fakeECOAuto{t: t, s: s, events: &events, quote: q}
		r, _ := NewECOPurchaseService(ECOPurchaseLive, client, steam, s, api, c5Candidates(1), 0)
		_, _ = r.Run(context.Background())
		if steam.calls != 1 || api.balances != 0 || api.buys != 0 {
			t.Fatal(events)
		}
	}
}

func TestECODryRunHasNoLedgerBalanceOrBuy(t *testing.T) {
	s := openTestStore(t)
	saveC5AState(t, s, 1)
	events := []string{}
	client, _ := runtimeTestClient(t, func(_ string, _ string, _ int) (int, string) { return 200, ecoDetail(1, "0.50", 400) })
	steam := &orderedSteam{events: &events, price: "1.00"}
	api := &fakeECOAuto{t: t, s: s, events: &events, quote: 50}
	r, _ := NewECOPurchaseService(ECOPurchaseDryRun, client, steam, s, api, c5Candidates(1), 0)
	result, err := r.Run(context.Background())
	if err != nil || result.DryRunCount != 1 || api.balances != 0 || api.buys != 0 {
		t.Fatal(result, err, events)
	}
	rows, err := s.ListECOPurchases(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	state, _, err := s.GetItemState(context.Background(), 1)
	if err != nil || state.SteamPrice == "1.00" {
		t.Fatal("purchase confirmation changed ordinary state", state, err)
	}
}

func TestECOUncertainBlocksEveryNewItemAndReconcilesOriginal(t *testing.T) {
	for _, detailFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(detailFailure), func(t *testing.T) {
			s := openTestStore(t)
			saveC5AState(t, s, 1)
			events := []string{}
			client, _ := runtimeTestClient(t, func(_ string, _ string, _ int) (int, string) { return 200, ecoDetail(1, "0.50", 400) })
			steam := &orderedSteam{events: &events, price: "1.00"}
			api := &fakeECOAuto{t: t, s: s, events: &events, quote: 50, balance: 1000, paid: 45, orderErr: errors.New("not found")}
			if !detailFailure {
				api.buyErr = errors.New("timeout")
			}
			r, _ := NewECOPurchaseService(ECOPurchaseLive, client, steam, s, api, c5Candidates(1, 2), 0)
			if _, err := r.Run(context.Background()); err == nil {
				t.Fatal("ambiguity accepted")
			}
			saveC5AState(t, s, 2)
			if _, err := r.Run(context.Background()); err == nil {
				t.Fatal("unresolved order accepted")
			}
			if api.buys != 1 || api.quotes != 1 {
				t.Fatal("new purchase during ambiguity")
			}
			api.orderErr = nil
			result, err := r.Run(context.Background())
			if err != nil || result.ReconciledCount != 1 || result.ECOPlacedPurchase == nil || api.buys != 1 {
				t.Fatal(result, err)
			}
		})
	}
}

func TestECOManualItemExcludedBeforeMarketRequests(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.InitECOTest(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.ReserveECOTest(ctx, "manual", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveECOOrder(ctx, p.ID, eco.Order{OrderNo: "123", MerchantNo: "manual", HashName: eco.TestHashName, Paid: 45, State: 1}); err != nil {
		t.Fatal(err)
	}
	saveC5AState(t, s, 1)
	state, _, _ := s.GetItemState(ctx, 1)
	state.MarketHashName = eco.TestHashName
	if err := s.SaveItemState(ctx, state); err != nil {
		t.Fatal(err)
	}
	candidates := c5Candidates(1)
	candidates[0].MarketHashName = eco.TestHashName
	client, requests := runtimeTestClient(t, func(_ string, _ string, _ int) (int, string) { return http.StatusInternalServerError, "{}" })
	events := []string{}
	steam := &orderedSteam{events: &events, price: "1.00"}
	api := &fakeECOAuto{t: t, s: s, events: &events}
	r, _ := NewECOPurchaseService(ECOPurchaseLive, client, steam, s, api, candidates, 0)
	result, err := r.Run(ctx)
	if err != nil || result.ExcludedCount != 1 || len(events) != 0 || requests.countPath("/api/commodity/1") != 0 {
		t.Fatal(result, err, events)
	}
}

func TestECOIncompleteScanNeverQuotesOrBuys(t *testing.T) {
	s := openTestStore(t)
	events := []string{}
	client, _ := runtimeTestClient(t, func(_ string, _ string, _ int) (int, string) { return http.StatusForbidden, "{}" })
	steam := &orderedSteam{events: &events}
	api := &fakeECOAuto{t: t, s: s, events: &events}
	r, _ := NewECOPurchaseService(ECOPurchaseLive, client, steam, s, api, c5Candidates(1), 0)
	config := runtimeTestConfig()
	config.Candidates = c5Candidates(1)
	config.Purchase = r
	if _, err := RunOneShot(context.Background(), client, s, config, nil); err == nil {
		t.Fatal("failed scan accepted")
	}
	if len(events) != 0 {
		t.Fatal(events)
	}
}

func TestECODueFailureNeverQuotesOrBuys(t *testing.T) {
	s := openTestStore(t)
	events := []string{}
	client, _ := runtimeTestClient(t, func(_ string, _ string, _ int) (int, string) { return http.StatusInternalServerError, "{}" })
	steam := &orderedSteam{events: &events}
	api := &fakeECOAuto{t: t, s: s, events: &events}
	r, _ := NewECOPurchaseService(ECOPurchaseLive, client, steam, s, api, c5Candidates(1), 0)
	state := dueState(1, time.Now().Add(-time.Minute))
	state.Tier = core.TierA
	saveState(t, s, state)
	config := runtimeTestConfig()
	config.Candidates = c5Candidates(1)
	config.Purchase = r
	config.InitialFullScanCompletedAt = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = Run(ctx, client, s, config, nil)
	if len(events) != 0 {
		t.Fatal(events)
	}
}

func TestECOSuccessReceiptAtExistingRuntimeBoundary(t *testing.T) {
	s := openTestStore(t)
	client, _ := runtimeTestClient(t, func(_ string, _ string, _ int) (int, string) { return 200, ecoDetail(1, "0.50", 400) })
	events := []string{}
	steam := &orderedSteam{events: &events, price: "1.00"}
	api := &fakeECOAuto{t: t, s: s, events: &events, quote: 50, balance: 1000, paid: 45}
	runner, _ := NewECOPurchaseService(ECOPurchaseLive, client, steam, s, api, c5Candidates(1), 0)
	config := runtimeTestConfig()
	config.Candidates = c5Candidates(1)
	config.Purchase = runner
	messages := []string{}
	sender := callbackSender(func(_ context.Context, a store.Alert) error { messages = append(messages, a.Message); return nil })
	result, err := RunOneShot(context.Background(), client, s, config, sender)
	if err != nil || result.Purchases.ECOPlacedPurchase == nil || len(messages) != 1 || !strings.Contains(messages[0], "ECO下单成功") || !strings.Contains(messages[0], "0.45") {
		t.Fatal(result, err, messages)
	}
	if steam.calls != 1 || api.buys != 1 {
		t.Fatal("scan or due checks contacted payment inputs", events)
	}
}
