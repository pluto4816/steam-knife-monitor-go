package sales

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestHTTPSBrowserSessionAndForms(t *testing.T) {
	s := testStore(t)
	server := httptest.NewUnstartedServer(nil)
	origin := "https://" + server.Listener.Addr().String()
	w, err := NewWeb(s, WebConfig{Listen: "127.0.0.1:8080", Origin: origin, Password: "https-test-password-12345"})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = w
	server.StartTLS()
	defer server.Close()
	client := server.Client() // Trust this server's certificate; no InsecureSkipVerify.
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	call := func(path string, values url.Values, want int) (string, http.Header) {
		t.Helper()
		method := http.MethodGet
		if values != nil {
			method = http.MethodPost
		}
		r, _ := http.NewRequest(method, origin+path, strings.NewReader(values.Encode()))
		if values != nil {
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		response, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		body, e := io.ReadAll(response.Body)
		if e != nil || response.StatusCode != want {
			t.Fatalf("%s: status %d, body %s, error %v", path, response.StatusCode, body, e)
		}
		return string(body), response.Header
	}
	call("/holdings", nil, 303)
	_, headers := call("/login", url.Values{"password": {w.config.Password}}, 303)
	cookie := headers.Get("Set-Cookie")
	for _, attr := range []string{"Secure", "HttpOnly", "SameSite=Strict"} {
		if !strings.Contains(cookie, attr) {
			t.Fatalf("missing %s", attr)
		}
	}
	httpURL, _ := url.Parse(strings.Replace(origin, "https:", "http:", 1))
	if len(client.Jar.Cookies(httpURL)) != 0 {
		t.Fatal("secure session sent over plaintext")
	}
	body, _ := call("/new", nil, 200)
	field := func(name string) string {
		t.Helper()
		m := regexp.MustCompile(`name="` + name + `" value="([a-f0-9]+)"`).FindStringSubmatch(body)
		if len(m) != 2 {
			t.Fatalf("missing form field %s", name)
		}
		return m[1]
	}
	csrf := field("csrf")
	v := url.Values{"csrf": {csrf}, "request_token": {field("request_token")}, "id": {"0"}, "version": {"0"}, "name": {"HTTPS 测试物品"}, "quantity": {"10"}, "unit_cost": {"0.60"}, "bought_on": {"2026-09-07"}, "target": {"0.6000"}, "quote": {"1.15"}, "status": {"held"}}
	call("/lots", v, 303)
	for _, path := range []string{"/new", "/holdings", "/sell-list", "/history", "/app.js", "/style.css"} {
		call(path, nil, 200)
	}
	call("/logout", url.Values{"csrf": {csrf}}, 303)
	call("/holdings", nil, 303)
}

func TestHTTPSConfigRejectsUnsafeCombinations(t *testing.T) {
	for _, tc := range []struct {
		name, listen, origin, cert, key string
		valid                           bool
	}{
		{"LAN TLS", ":8443", "https://192.168.1.10:8443", "cert.pem", "key.pem", true},
		{"HTTPS proxy on loopback", "127.0.0.1:8080", "https://sales.example.com", "", "", true},
		{"LAN plaintext", ":8080", "http://192.168.1.10:8080", "", "", false},
		{"HTTPS origin cannot authorize plaintext LAN listener", ":8080", "https://sales.example.com", "", "", false},
		{"TLS with HTTP origin", ":8443", "http://127.0.0.1:8443", "cert.pem", "key.pem", false},
		{"missing TLS key", ":8443", "https://192.168.1.10:8443", "cert.pem", "", false},
		{"origin path", "127.0.0.1:8080", "https://sales.example.com/holdings", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := WebConfig{Listen: tc.listen, Origin: tc.origin, CertFile: tc.cert, KeyFile: tc.key, Password: "https-test-password-12345"}
			if err := c.Validate(); (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
