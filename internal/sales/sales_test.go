package sales

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "sales.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func sample() Lot {
	return Lot{Name: "P250 | 重构（久经沙场）", HashName: "P250 | Re.built (Field-Tested)", Platform: "ECO", BoughtOn: "2026-09-01", Quantity: 3, UnitCost: 60, Target: 6000, Quote: 115, Status: "held"}
}
func TestFormulaExactAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		cost, gross int64
		want        int32
	}{{60, 115, 6000}, {45, 115, 4500}, {60, 100, 6900}, {1, 1, 11500}, {1, 32, 359}, {1, 8, 1437}} {
		got, e := Discount(tc.cost, tc.gross)
		if e != nil || int32(got) != tc.want {
			t.Fatalf("%+v: %d %v", tc, got, e)
		}
	}
	l := sample()
	if l.TargetGross() != 115 || l.TargetNet() != 100 || !l.Ready() || l.EstimatedNet() != 300 {
		t.Fatalf("bad projection: %+v", l)
	}
	l.UnitCost = 10000
	l.Quote = 19166
	if l.DiscountText() != "0.6000" || l.Ready() {
		t.Fatal("rounded display must not imply target eligibility")
	}
	l.UnitCost = 61
	if l.TargetGross() != 117 {
		t.Fatal("target gross must round up")
	}
	l.Quote = 116
	if l.Ready() {
		t.Fatal("rounded ratio must not make a below-target price eligible")
	}
	for _, s := range []string{"-1", "NaN", "1e2", "0.001", ".5", "1.", "99999999999999999999", "10000000.01"} {
		if _, e := ParseMoney(s); e == nil {
			t.Errorf("accepted %q", s)
		}
	}
	if n, e := ParseMoney("0.45"); e != nil || n != 45 {
		t.Fatal(n, e)
	}
	if _, e := Discount(0, 100); e == nil {
		t.Fatal("accepted zero cost")
	}
	if _, e := Discount(MaxMoney, 1); e == nil {
		t.Fatal("accepted overflowing ratio")
	}
}
func TestLedgerPartialSaleIdempotenceCorrectionAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	buyToken := randomToken()
	for i := 0; i < 2; i++ {
		if e = s.SaveLot(ctx, buyToken, sample()); e != nil {
			t.Fatal(e)
		}
	}
	lots, _ := s.Lots(ctx)
	if len(lots) != 1 {
		t.Fatal("duplicate buy")
	}
	l := lots[0]
	sellToken := randomToken()
	for i := 0; i < 2; i++ {
		if e = s.Sell(ctx, sellToken, l.ID, l.Version, 2, 190, "2026-09-02"); e != nil {
			t.Fatal(e)
		}
	}
	lots, _ = s.Lots(ctx)
	if lots[0].Remaining() != 1 {
		t.Fatal("incorrect remaining")
	}
	receipts, _ := s.Receipts(ctx)
	if len(receipts) != 1 || receipts[0].DiscountText() != "0.6316" {
		t.Fatalf("receipts: %+v", receipts)
	}
	updated := lots[0]
	updated.UnitCost = 70
	if e = s.SaveLot(ctx, randomToken(), updated); e == nil {
		t.Fatal("rewrote realized cost")
	}
	if e = s.Sell(ctx, randomToken(), l.ID, l.Version, 1, 100, "2026-09-03"); e == nil {
		t.Fatal("stale update accepted")
	}
	if e = s.Sell(ctx, randomToken(), l.ID, lots[0].Version, 2, 100, "2026-09-03"); e == nil {
		t.Fatal("oversold")
	}
	voidToken := randomToken()
	for i := 0; i < 2; i++ {
		if e = s.VoidSale(ctx, voidToken, receipts[0].ID); e != nil {
			t.Fatal(e)
		}
	}
	lots, _ = s.Lots(ctx)
	if lots[0].Remaining() != 3 {
		t.Fatal("void did not restore")
	}
	if e = s.VoidSale(ctx, randomToken(), receipts[0].ID); e == nil {
		t.Fatal("double void accepted")
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	lots, _ = s.Lots(ctx)
	receipts, _ = s.Receipts(ctx)
	if len(lots) != 1 || lots[0].Remaining() != 3 || !receipts[0].Voided {
		t.Fatal("restart lost ledger")
	}
}
func TestConcurrentSaleCannotOversell(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	l := sample()
	l.Quantity = 1
	if e := s.SaveLot(ctx, randomToken(), l); e != nil {
		t.Fatal(e)
	}
	lots, _ := s.Lots(ctx)
	l = lots[0]
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.Sell(ctx, randomToken(), l.ID, l.Version, 1, 100, "2026-09-02") }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successes=%d", successes)
	}
}
func webTest(t *testing.T) (*Web, *Store) {
	t.Helper()
	s := testStore(t)
	w, e := NewWeb(s, WebConfig{Listen: "127.0.0.1:8080", Origin: "http://127.0.0.1:8080", Password: "test-password-123456"})
	if e != nil {
		t.Fatal(e)
	}
	return w, s
}
func request(w *Web, method, path string, values url.Values, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rw := httptest.NewRecorder()
	w.ServeHTTP(rw, r)
	return rw
}
func TestWebLoginCSRFRenderingAndPostFlow(t *testing.T) {
	w, s := webTest(t)
	origin := w.config.Origin
	if r := request(w, "GET", "/holdings", nil, nil, ""); r.Code != 303 {
		t.Fatal(r.Code)
	}
	login := request(w, "POST", "/login", url.Values{"password": {w.config.Password}}, nil, origin)
	if login.Code != 303 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("weak cookie")
	}
	csrf := w.sign("csrf|" + cookie.Value)
	v := url.Values{"csrf": {csrf}, "request_token": {randomToken()}, "id": {"0"}, "version": {"0"}, "name": {"<script>alert(1)</script>"}, "quantity": {"3"}, "unit_cost": {"0.60"}, "target": {"0.6000"}, "quote": {"1.15"}, "bought_on": {"2026-09-01"}, "status": {"held"}}
	for _, attackOrigin := range []string{"https://evil.example", ""} {
		if r := request(w, "POST", "/lots", v, cookie, attackOrigin); r.Code != 403 {
			t.Fatal("accepted cross origin")
		}
	}
	bad := url.Values{"csrf": {"wrong"}}
	if r := request(w, "POST", "/lots", bad, cookie, origin); r.Code != 403 {
		t.Fatal("accepted bad csrf")
	}
	for i := 0; i < 2; i++ {
		if r := request(w, "POST", "/lots", v, cookie, origin); r.Code != 303 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	lots, _ := s.Lots(context.Background())
	if len(lots) != 1 {
		t.Fatal("duplicate web submission")
	}
	for _, path := range []string{"/new", "/holdings", "/sell-list", "/history", "/new?edit=1"} {
		r := request(w, "GET", path, nil, cookie, "")
		if r.Code != 200 || !strings.Contains(r.Body.String(), "</html>") {
			t.Fatalf("render %s: %d %s", path, r.Code, r.Body.String())
		}
		if strings.Contains(r.Body.String(), "<script>alert") {
			t.Fatal("XSS")
		}
		if r.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("sensitive page cached")
		}
	}
	r := request(w, "GET", "/sell-list", nil, cookie, "")
	if !strings.Contains(r.Body.String(), "¥1.15") || !strings.Contains(r.Body.String(), "0.6000") {
		t.Fatal("formula display missing")
	}
	sale := url.Values{"csrf": {csrf}, "request_token": {randomToken()}, "id": {"1"}, "version": {"1"}, "quantity": {"1"}, "received": {"1.00"}, "sold_on": {"2026-09-02"}}
	if r = request(w, "POST", "/sell", sale, cookie, origin); r.Code != 303 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(w, "GET", "/history", nil, cookie, "")
	if !strings.Contains(r.Body.String(), "作废这笔登记") || !strings.Contains(r.Body.String(), "</html>") {
		t.Fatal("history incomplete")
	}
	if r = request(w, "GET", "/sell", nil, cookie, ""); r.Code != 404 {
		t.Fatal("GET mutation")
	}
	if r = request(w, "POST", "/logout", url.Values{"csrf": {csrf}}, cookie, origin); r.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout failed")
	}
}
func TestWebConfigAndLoginThrottling(t *testing.T) {
	w, _ := webTest(t)
	for _, c := range []WebConfig{
		{Listen: "0.0.0.0:8080", Origin: "http://example.com", Password: w.config.Password},
		{Listen: "127.0.0.1:8080", Origin: "http://127.0.0.1:8080", Password: "short"},
		{Listen: "127.0.0.1:8080", Origin: "https://example.com/path", Password: w.config.Password},
	} {
		if e := c.Validate(); e == nil {
			t.Fatalf("accepted config %+v", c)
		}
	}
	for i := 0; i < 5; i++ {
		r := request(w, "POST", "/login", url.Values{"password": {"bad"}}, nil, w.config.Origin)
		if r.Code != 401 {
			t.Fatal(r.Code)
		}
	}
	if r := request(w, "POST", "/login", url.Values{"password": {w.config.Password}}, nil, w.config.Origin); r.Code != 429 {
		t.Fatal("no throttle")
	}
}
func TestTemplatesHaveUniqueSubmissionTokens(t *testing.T) {
	w, s := webTest(t)
	for i := 0; i < 2; i++ {
		if e := s.SaveLot(context.Background(), randomToken(), sample()); e != nil {
			t.Fatal(e)
		}
	}
	login := request(w, "POST", "/login", url.Values{"password": {w.config.Password}}, nil, w.config.Origin)
	cookie := login.Result().Cookies()[0]
	r := request(w, "GET", "/sell-list", nil, cookie, "")
	matches := regexp.MustCompile(`name="request_token" value="([a-f0-9]+)"`).FindAllStringSubmatch(r.Body.String(), -1)
	if len(matches) != 2 || matches[0][1] == matches[1][1] {
		t.Fatal(fmt.Sprintf("tokens: %v", matches))
	}
}

func TestQuoteTimestampRefreshAndTableIsolation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, e := s.db.Exec(`CREATE TABLE existing_monitor_data(value TEXT); INSERT INTO existing_monitor_data VALUES('untouched')`); e != nil {
		t.Fatal(e)
	}
	if e := s.SaveLot(ctx, randomToken(), sample()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE sales_lots SET quote_at='2020-01-01T00:00:00Z'`); e != nil {
		t.Fatal(e)
	}
	lots, _ := s.Lots(ctx)
	l := lots[0]
	if l.Fresh() {
		t.Fatal("stale quote fresh")
	}
	l.Target = 6500
	if e := s.SaveLot(ctx, randomToken(), l); e != nil {
		t.Fatal(e)
	}
	lots, _ = s.Lots(ctx)
	l = lots[0]
	if l.Fresh() {
		t.Fatal("unrelated edit refreshed quote")
	}
	l.RefreshQuote = true
	if e := s.SaveLot(ctx, randomToken(), l); e != nil {
		t.Fatal(e)
	}
	lots, _ = s.Lots(ctx)
	if !lots[0].Fresh() {
		t.Fatal("explicit recheck did not refresh quote")
	}
	var v string
	if e := s.db.QueryRow(`SELECT value FROM existing_monitor_data`).Scan(&v); e != nil || v != "untouched" {
		t.Fatal("modified unrelated table")
	}
	var count int
	if e := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('c5_purchases','eco_purchases','item_state')`).Scan(&count); e != nil || count != 0 {
		t.Fatal("initialized monitoring/purchasing tables")
	}
}
