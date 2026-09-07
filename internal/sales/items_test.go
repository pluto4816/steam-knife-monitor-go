package sales

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
)

func TestCatalogNameResolution(t *testing.T) {
	w, _ := webTest(t)
	w.choices = []catalog.Item{
		{ItemID: 1, NameCN: "P250 | 重构 (久经沙场)", MarketHashName: "P250 | Re.built (Field-Tested)"},
		{ItemID: 2, NameCN: "测试枪 | 重构（久经沙场）", MarketHashName: "Test | Re.built (Field-Tested)"},
	}
	for _, tc := range []struct{ name, selected string }{{"P250 | 重构", "1"}, {"P250 | 重构", ""}, {w.choices[0].NameCN, ""}} {
		name, hash, err := w.resolveItem(tc.name, "wrong browser hash", tc.selected)
		if err != nil || name != w.choices[0].NameCN || hash != w.choices[0].MarketHashName {
			t.Fatalf("catalog identity not resolved: %s %s %v", name, hash, err)
		}
	}
	for _, tc := range []struct{ name, selected string }{{"重构", ""}, {"P250 | 重构", "2"}, {"P250 | 重构", "999"}} {
		if _, _, err := w.resolveItem(tc.name, "", tc.selected); err == nil {
			t.Fatal("accepted ambiguous or stale selection", tc)
		}
	}
	if shortName("物品（崭新出厂）") != "物品（崭新出厂）" {
		t.Fatal("removed non-default wear")
	}
}

func TestSimplifiedFormPreservesExistingQuoteAndHistory(t *testing.T) {
	w, s := webTest(t)
	ctx := context.Background()
	if err := s.SaveLot(ctx, randomToken(), sample()); err != nil {
		t.Fatal(err)
	}
	if err := s.Sell(ctx, randomToken(), 1, 1, 1, 100, "2026-09-02"); err != nil {
		t.Fatal(err)
	}
	lots, _ := s.Lots(ctx)
	old := lots[0]
	login := request(w, "POST", "/login", url.Values{"password": {w.config.Password}}, nil, w.config.Origin)
	cookie := login.Result().Cookies()[0]
	csrf := w.sign("csrf|" + cookie.Value)
	page := request(w, "GET", "/new", nil, cookie, "").Body.String()
	if !strings.Contains(page, `value="0.6500"`) || strings.Contains(page, `name="quote"`) || strings.Contains(page, `name="refresh_quote"`) {
		t.Fatal("new form default or removed fields incorrect")
	}
	v := url.Values{"csrf": {csrf}, "request_token": {randomToken()}, "id": {"1"}, "version": {"2"}, "name": {shortName(old.Name)}, "hash_name": {old.HashName}, "quantity": {"3"}, "unit_cost": {"0.60"}, "bought_on": {old.BoughtOn}, "target": {"0.6500"}, "status": {"held"}, "platform": {old.Platform}}
	res := request(w, "POST", "/lots", v, cookie, w.config.Origin)
	if res.Code != 303 {
		t.Fatal(res.Code, res.Body.String())
	}
	lots, _ = s.Lots(ctx)
	if lots[0].Quote != old.Quote || lots[0].QuoteAt != old.QuoteAt || lots[0].Name != old.Name || lots[0].Target != 6500 {
		t.Fatal("edit rewrote identity/quote or lost target", lots[0])
	}
	quote := url.Values{"csrf": {csrf}, "request_token": {randomToken()}, "id": {"1"}, "version": {"3"}, "quote": {"1.20"}}
	for i := 0; i < 2; i++ {
		if r := request(w, "POST", "/quote", quote, cookie, w.config.Origin); r.Code != 303 || r.Header().Get("Location") != "/sell-list" {
			t.Fatal("quote save or replay failed", r.Code)
		}
	}
	lots, _ = s.Lots(ctx)
	if lots[0].Version != 4 || lots[0].Quote != 120 || lots[0].Sold != 1 || lots[0].UnitCost != 60 {
		t.Fatal("quote update altered accounting or repeated", lots[0])
	}
	quote.Set("request_token", randomToken())
	if r := request(w, "POST", "/quote", quote, cookie, w.config.Origin); r.Code != 400 {
		t.Fatal("stale quote accepted")
	}
}

func TestExistingManualNameStillEditableAfterCatalogLoads(t *testing.T) {
	w, s := webTest(t)
	l := sample()
	l.Name, l.HashName = "P250", ""
	if err := s.SaveLot(context.Background(), randomToken(), l); err != nil {
		t.Fatal(err)
	}
	w.choices = []catalog.Item{{ItemID: 1, NameCN: "P250 | 重构 (久经沙场)", MarketHashName: "P250 | Re.built (Field-Tested)"}}
	login := request(w, "POST", "/login", url.Values{"password": {w.config.Password}}, nil, w.config.Origin)
	cookie := login.Result().Cookies()[0]
	v := url.Values{"csrf": {w.sign("csrf|" + cookie.Value)}, "request_token": {randomToken()}, "id": {"1"}, "version": {"1"}, "name": {l.Name}, "quantity": {"3"}, "unit_cost": {"0.60"}, "bought_on": {l.BoughtOn}, "target": {"0.6500"}, "status": {"held"}, "platform": {l.Platform}}
	res := request(w, "POST", "/lots", v, cookie, w.config.Origin)
	if res.Code != 303 {
		t.Fatal(res.Code, res.Body.String())
	}
	lots, _ := s.Lots(context.Background())
	if lots[0].Name != "P250" || lots[0].HashName != "" || lots[0].Target != 6500 {
		t.Fatal("catalog rewrote existing manual identity", lots[0])
	}
}
