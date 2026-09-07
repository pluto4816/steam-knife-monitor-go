package sales

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

//go:embed page.html
var pageHTML string

//go:embed style.css
var styleCSS string

//go:embed app.js
var appJS string

type WebConfig struct{ Listen, Origin, CertFile, KeyFile, Password, CandidatePath string }

func (c WebConfig) Validate() error {
	if len(c.Password) < 16 {
		return fmt.Errorf("SALES_PASSWORD must contain at least 16 characters")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("invalid sales listen address")
	}
	if (c.CertFile == "") != (c.KeyFile == "") {
		return fmt.Errorf("both TLS certificate and key are required")
	}
	ip := net.ParseIP(host)
	if c.CertFile == "" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("plain HTTP must bind a loopback IP; use TLS for remote access")
	}
	u, err := url.Parse(c.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("sales origin must be an exact http(s) origin without a path")
	}
	if u.Scheme == "http" {
		p := net.ParseIP(u.Hostname())
		if c.CertFile != "" || (u.Hostname() != "localhost" && (p == nil || !p.IsLoopback())) {
			return fmt.Errorf("remote sales origin must use HTTPS")
		}
	}
	return nil
}

type Web struct {
	store        *Store
	config       WebConfig
	key          []byte
	tpl          *template.Template
	choices      []catalog.Item
	authMu       sync.Mutex
	failed       int
	blockedUntil time.Time
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure randomness unavailable")
	}
	return hex.EncodeToString(b)
}
func NewWeb(s *Store, c WebConfig) (*Web, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte("sales-session|" + c.Password))
	t, err := template.New("page").Funcs(template.FuncMap{"money": Money, "ratio": Ratio, "token": randomToken}).Parse(pageHTML)
	if err != nil {
		return nil, err
	}
	w := &Web{store: s, config: c, key: sum[:], tpl: t}
	if c.CandidatePath != "" {
		if cat, err := catalog.Load(c.CandidatePath); err == nil {
			w.choices = cat.Items
		} else {
			log.Print("sales catalog unavailable; manual identity entry remains available")
		}
	}
	return w, nil
}
func (w *Web) sign(value string) string {
	h := hmac.New(sha256.New, w.key)
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}
func (w *Web) session(r *http.Request) string {
	cookie, err := r.Cookie("sales_session")
	if err != nil {
		return ""
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 {
		return ""
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || expires <= time.Now().Unix() || expires > time.Now().Add(13*time.Hour).Unix() {
		return ""
	}
	if !hmac.Equal([]byte(parts[2]), []byte(w.sign(parts[0]+"."+parts[1]))) {
		return ""
	}
	return cookie.Value
}
func (w *Web) cookie(value string, age int) *http.Cookie {
	return &http.Cookie{Name: "sales_session", Value: value, Path: "/", MaxAge: age, HttpOnly: true, Secure: strings.HasPrefix(w.config.Origin, "https://"), SameSite: http.SameSiteStrictMode}
}
func (w *Web) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.Header().Set("Referrer-Policy", "same-origin")
	rw.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if r.Method != "GET" && r.Method != "POST" {
		rw.Header().Set("Allow", "GET, POST")
		http.Error(rw, "不支持的请求方式", 405)
		return
	}
	if r.URL.Path == "/app.js" && r.Method == "GET" {
		rw.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		fmt.Fprint(rw, appJS)
		return
	}
	if r.URL.Path == "/style.css" && r.Method == "GET" {
		rw.Header().Set("Content-Type", "text/css; charset=utf-8")
		fmt.Fprint(rw, styleCSS)
		return
	}
	if r.Method == "POST" {
		if r.Header.Get("Origin") != w.config.Origin {
			http.Error(rw, "页面来源不匹配，请从配置的访问地址打开", 403)
			return
		}
		r.Body = http.MaxBytesReader(rw, r.Body, 16384)
		if err := r.ParseForm(); err != nil {
			http.Error(rw, "表单内容无效或过大", 400)
			return
		}
	}
	if r.URL.Path == "/login" {
		w.login(rw, r)
		return
	}
	session := w.session(r)
	if session == "" {
		if r.Method == "POST" {
			http.Error(rw, "登录已过期，请重新登录", 401)
		} else {
			http.Redirect(rw, r, "/login", 303)
		}
		return
	}
	csrf := w.sign("csrf|" + session)
	if r.Method == "POST" {
		if !hmac.Equal([]byte(r.PostForm.Get("csrf")), []byte(csrf)) {
			http.Error(rw, "表单已失效，请刷新页面", 403)
			return
		}
		if r.URL.Path == "/logout" {
			http.SetCookie(rw, w.cookie("", -1))
			http.Redirect(rw, r, "/login", 303)
			return
		}
		w.mutation(rw, r, csrf)
		return
	}
	w.page(rw, r, csrf, "", 200)
}
func (w *Web) login(rw http.ResponseWriter, r *http.Request) {
	message := ""
	if r.Method == "POST" {
		w.authMu.Lock()
		blocked := time.Now().Before(w.blockedUntil)
		if blocked {
			w.authMu.Unlock()
			rw.Header().Set("Retry-After", "60")
			http.Error(rw, "尝试次数较多，请稍后再试", 429)
			return
		}
		got := sha256.Sum256([]byte(r.PostForm.Get("password")))
		want := sha256.Sum256([]byte(w.config.Password))
		ok := subtle.ConstantTimeCompare(got[:], want[:]) == 1
		if ok {
			w.failed = 0
		} else {
			w.failed++
			if w.failed >= 5 {
				w.blockedUntil = time.Now().Add(time.Minute)
				w.failed = 0
			}
		}
		w.authMu.Unlock()
		if ok {
			v := strconv.FormatInt(time.Now().Add(12*time.Hour).Unix(), 10) + "." + randomToken()
			v += "." + w.sign(v)
			http.SetCookie(rw, w.cookie(v, 43200))
			http.Redirect(rw, r, "/holdings", 303)
			return
		}
		message = "密码不正确"
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if message != "" {
		rw.WriteHeader(401)
	}
	if err := w.tpl.Execute(rw, pageData{Page: "login", Title: "登录", Error: message}); err != nil {
		log.Print("sales login rendering failed")
	}
}

type pageData struct {
	Page, Title, CSRF, Error, Today, Query      string
	Lots                                        []Lot
	Sales                                       []Sale
	Choices                                     []catalog.Item
	Edit                                        Lot
	Editing                                     bool
	Count, Cost, Received, SoldCost, ReadyCount int64
}

func (w *Web) page(rw http.ResponseWriter, r *http.Request, csrf, message string, status int) {
	pages := map[string]string{"/holdings": "持仓", "/new": "新增买入", "/sell-list": "待售清单", "/history": "买卖记录"}
	path := r.URL.Path
	if path == "/" {
		http.Redirect(rw, r, "/holdings", 303)
		return
	}
	title, ok := pages[path]
	if !ok {
		http.NotFound(rw, r)
		return
	}
	lots, err := w.store.Lots(r.Context())
	if err != nil {
		http.Error(rw, "读取持仓失败，请稍后再试", 500)
		return
	}
	receipts, err := w.store.Receipts(r.Context())
	if err != nil {
		http.Error(rw, "读取买卖记录失败，请稍后再试", 500)
		return
	}
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
	d := pageData{Page: strings.TrimPrefix(path, "/"), Title: title, CSRF: csrf, Error: message, Today: day, Choices: w.choices, Sales: receipts, Query: strings.TrimSpace(r.URL.Query().Get("q")), Edit: Lot{Quantity: 1, Target: 6000, BoughtOn: day, Status: "held"}}
	id, _ := strconv.ParseInt(r.URL.Query().Get("edit"), 10, 64)
	for _, l := range lots {
		d.Count += l.Remaining()
		d.Cost += l.Cost()
		if l.Ready() && l.Fresh() && l.Status == "held" {
			d.ReadyCount++
		}
		if l.ID == id {
			d.Edit = l
			d.Editing = true
		}
		if d.Query != "" && !strings.Contains(strings.ToLower(l.Name+" "+l.HashName+" "+l.Platform), strings.ToLower(d.Query)) {
			continue
		}
		if path == "/history" || l.Remaining() > 0 {
			d.Lots = append(d.Lots, l)
		}
	}
	if path == "/new" && id > 0 && !d.Editing {
		http.NotFound(rw, r)
		return
	}
	for _, s := range receipts {
		if !s.Voided {
			d.Received += s.Received
			d.SoldCost += s.Cost()
		}
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(status)
	if err := w.tpl.Execute(rw, d); err != nil {
		log.Print("sales page rendering failed")
	}
}
func intForm(r *http.Request, key string) (int64, error) {
	return strconv.ParseInt(r.PostForm.Get(key), 10, 64)
}
func (w *Web) mutation(rw http.ResponseWriter, r *http.Request, csrf string) {
	var err error
	token := r.PostForm.Get("request_token")
	switch r.URL.Path {
	case "/lots":
		var l Lot
		l.Name = strings.TrimSpace(r.PostForm.Get("name"))
		l.HashName = strings.TrimSpace(r.PostForm.Get("hash_name"))
		l.Platform = strings.TrimSpace(r.PostForm.Get("platform"))
		l.BoughtOn = r.PostForm.Get("bought_on")
		l.Status = r.PostForm.Get("status")
		l.RefreshQuote = r.PostForm.Get("refresh_quote") == "yes"
		l.ID, _ = intForm(r, "id")
		l.Version, _ = intForm(r, "version")
		if l.Quantity, err = intForm(r, "quantity"); err == nil {
			l.UnitCost, err = ParseMoney(r.PostForm.Get("unit_cost"))
		}
		if err == nil {
			l.Target, err = core.ParseRatio(r.PostForm.Get("target"))
		}
		if err == nil && r.PostForm.Get("quote") != "" {
			l.Quote, err = ParseMoney(r.PostForm.Get("quote"))
		}
		if err == nil {
			err = w.store.SaveLot(r.Context(), token, l)
		}
	case "/sell":
		id, e1 := intForm(r, "id")
		version, e2 := intForm(r, "version")
		qty, e3 := intForm(r, "quantity")
		received, e4 := ParseMoney(r.PostForm.Get("received"))
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			err = fmt.Errorf("请填写正确的出售数量和实际总到账")
		} else {
			err = w.store.Sell(r.Context(), token, id, version, qty, received, r.PostForm.Get("sold_on"))
		}
	case "/void-sale":
		id, e := intForm(r, "id")
		if e != nil {
			err = fmt.Errorf("记录编号无效")
		} else {
			err = w.store.VoidSale(r.Context(), token, id)
		}
	default:
		http.NotFound(rw, r)
		return
	}
	if err != nil {
		// Keep the browser on its form so entered values are recoverable with Back.
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.WriteHeader(400)
		if e := w.tpl.Execute(rw, pageData{Page: "error", Title: "暂未保存", CSRF: csrf, Error: err.Error()}); e != nil {
			log.Print("sales error rendering failed")
		}
		return
	}
	dest := "/holdings"
	if r.URL.Path == "/sell" || r.URL.Path == "/void-sale" {
		dest = "/history"
	}
	http.Redirect(rw, r, dest, 303)
}
func Serve(ctx context.Context, s *Store, c WebConfig) error {
	w, err := NewWeb(s, c)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: c.Listen, Handler: w, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdown)
		case <-done:
		}
	}()
	log.Printf("selling assistant ready: %s", c.Origin)
	if c.CertFile != "" {
		err = server.ListenAndServeTLS(c.CertFile, c.KeyFile)
	} else {
		err = server.ListenAndServe()
	}
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
