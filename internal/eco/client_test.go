package eco

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

func TestMoneyExact(t *testing.T) {
	for input, want := range map[string]MoneyFen{"0": 0, "0.01": 1, "0.1": 10, "1.00": 100, "123456.78": 12345678, "0.5000": 50} {
		got, err := ParseMoney(input)
		if err != nil || got != want {
			t.Fatalf("%s: %v %v", input, got, err)
		}
	}
	for _, input := range []string{"", "null", "-1", "NaN", "1e0", "1.001", "1.", " 1", ".1", "999999999999999999999999"} {
		if _, err := ParseMoney(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func TestSigningOfficialCaseInsensitiveOrder(t *testing.T) {
	key, _ := testKey(t)
	sig, err := signFields(map[string]any{"Timestamp": "1700000000", "PartnerId": "test", "idempotentId": "x", "Price": json.Number("1.00")}, key)
	if err != nil {
		t.Fatal(err)
	}
	// Independent literal based on official Python sorted(keys, key=str.lower).
	digest := sha256.Sum256([]byte("idempotentId=x&PartnerId=test&Price=1.00&Timestamp=1700000000"))
	decoded, _ := base64.StdEncoding.DecodeString(sig)
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], decoded); err != nil {
		t.Fatal(err)
	}
}

func TestClientPaymentContract(t *testing.T) {
	_, key := testKey(t)
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.Method != "POST" || len(body["Sign"]) == 0 {
			t.Error("unsigned or wrong method")
		}
		switch r.URL.Path {
		case balancePath:
			io.WriteString(w, `{"ResultCode":"0","ResultData":{"Money":10.25,"LockMoney":0.25}}`)
		case buyPath:
			if string(body["Price"]) != "1" || string(body["MaxCount"]) != "1" || string(body["HashName"]) != `"P250 | Re.built (Field-Tested)"` {
				t.Errorf("wrong purchase limits")
			}
			for _, field := range []string{"IsFirst", "StyleName", "ServiceProviderId", "OpenID"} {
				if _, ok := body[field]; ok {
					t.Errorf("unexpected %s", field)
				}
			}
			io.WriteString(w, `{"ResultCode":0,"ResultData":[{"OrderNo":"123456","MerchantNo":"test-order","PaymentPrice":0.36,"OrderIdList":[]}]}`)
		case orderPath:
			if string(body["MerchantNo"]) != `"test-order"` {
				t.Error("wrong original merchant number")
			}
			io.WriteString(w, `{"ResultCode":0,"ResultData":{"OrderNum":"123456","MerchantNo":"test-order","HashName":"P250 | Re.built (Field-Tested)","TotalMoney":0.36,"BuyerFee":0,"OrderStateCode":1}}`)
		default:
			t.Error("unexpected external path")
		}
	}))
	defer server.Close()
	c, err := NewClient("private-partner", key, "https://steamcommunity.com/tradeoffer/new/?partner=123&token=secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	c.baseURL = server.URL
	if got, err := c.Balance(context.Background()); err != nil || got != 1000 {
		t.Fatalf("balance %v %v", got, err)
	}
	if got, err := c.BuyTestItem(context.Background(), "test-order"); err != nil || got.Paid != 36 {
		t.Fatalf("buy %v %v", got, err)
	}
	if got, err := c.Order(context.Background(), "test-order"); err != nil || got.State != 1 || got.Paid != 36 {
		t.Fatalf("order %v %v", got, err)
	}
	if len(paths) != 3 {
		t.Fatal(paths)
	}
}

func TestPaymentRejectsAmbiguityWithoutRetryOrCredentialEcho(t *testing.T) {
	_, key := testKey(t)
	cases := []string{
		``, `<html>challenge</html>`, `{}`, `{"ResultCode":null}`, `{"ResultCode":0,"ResultData":null}`,
		`{"ResultCode":6003,"ResultMsg":"secret-partner secret-token"}`,
		`{"ResultCode":0,"ResultData":[]}`,
		`{"ResultCode":0,"ResultData":[{"OrderNo":"1","MerchantNo":"wrong","PaymentPrice":0.1}]}`,
		`{"ResultCode":0,"ResultData":[{"OrderNo":"1","MerchantNo":"test","PaymentPrice":1.01}]}`,
		`{"ResultCode":0,"ResultData":[{"OrderNo":"1","MerchantNo":"test","PaymentPrice":0}]}`,
		`{"ResultCode":0,"ResultData":[{"OrderNo":"1","MerchantNo":"test","PaymentPrice":0.123}]}`,
		`{"ResultCode":0,"ResultData":[{"OrderNo":"1","MerchantNo":"test","PaymentPrice":0.1,"OrderIdList":["1"]}]}`,
		`{"ResultCode":0,"ResultData":[{"OrderNo":"1","MerchantNo":"test","PaymentPrice":0.1},{"OrderNo":"2","MerchantNo":"test","PaymentPrice":0.1}]}`,
	}
	for _, body := range cases {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, body) }))
		c, err := NewClient("secret-partner", key, "https://steamcommunity.com/tradeoffer/new/?partner=123&token=secret-token", server.Client())
		if err != nil {
			t.Fatal(err)
		}
		c.baseURL = server.URL
		_, err = c.BuyTestItem(context.Background(), "test")
		server.Close()
		if err == nil || calls != 1 {
			t.Fatalf("accepted or retried: %q %v calls=%d", body, err, calls)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("credential leaked")
		}
	}
}

func TestNoRedirectOrHTTPRetry(t *testing.T) {
	_, key := testKey(t)
	for _, status := range []int{301, 302, 307, 308, 401, 403, 429, 500} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Location", "/redirect")
			w.WriteHeader(status)
		}))
		c, _ := NewClient("test", key, "https://steamcommunity.com/tradeoffer/new/?partner=123&token=test", server.Client())
		c.baseURL = server.URL
		_, err := c.BuyTestItem(context.Background(), "test")
		server.Close()
		if err == nil || calls != 1 {
			t.Fatalf("HTTP %d err=%v calls=%d", status, err, calls)
		}
	}
}

func TestPrivateKeyFormatsAndInvalidKeys(t *testing.T) {
	key, pkcs1PEM := testKey(t)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{
		pkcs1PEM,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}),
		[]byte(base64.StdEncoding.EncodeToString(pkcs8)),
		[]byte(base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(key))),
	} {
		got, err := parsePrivateKey(data)
		if err != nil || got.N.Cmp(key.N) != 0 {
			t.Fatal("key decode failed", err)
		}
	}
	for _, data := range [][]byte{nil, []byte("not-a-key"), pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)})} {
		if _, err := parsePrivateKey(data); err == nil {
			t.Fatal("accepted invalid/private-less key")
		}
	}
}

func TestOrderRejectsIdentityAmountsAndAbsentState(t *testing.T) {
	_, key := testKey(t)
	valid := `{"ResultCode":0,"ResultData":{"OrderNum":"123","MerchantNo":"test","HashName":"P250 | Re.built (Field-Tested)","TotalMoney":0.36,"BuyerFee":0,"OrderStateCode":1}}`
	for _, body := range []string{
		strings.Replace(valid, `"MerchantNo":"test"`, `"MerchantNo":"other"`, 1),
		strings.Replace(valid, "Field-Tested", "Minimal Wear", 1),
		strings.Replace(valid, `"TotalMoney":0.36`, `"TotalMoney":1.01`, 1),
		strings.Replace(valid, `"BuyerFee":0`, `"BuyerFee":0.65`, 1),
		strings.Replace(valid, `"OrderStateCode":1`, `"OrderStateCode":null`, 1),
		strings.Replace(valid, `"TotalMoney":0.36`, `"TotalMoney":null`, 1),
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		c, err := NewClient("test", key, "https://steamcommunity.com/tradeoffer/new/?partner=123&token=test", server.Client())
		if err != nil {
			t.Fatal(err)
		}
		c.baseURL = server.URL
		_, err = c.Order(context.Background(), "test")
		server.Close()
		if err == nil {
			t.Fatal("accepted invalid order", body)
		}
	}
}

func TestECOQuoteArraySignatureAndVariableBuyCeiling(t *testing.T) {
	key, keyData := testKey(t)
	hash := "P250 | Re.built (Field-Tested)"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path == quotePath {
			if string(body["GameID"]) != `"730"` || string(body["HashName"]) != `["P250 | Re.built (Field-Tested)"]` {
				t.Fatal("incorrect quote selector")
			}
			var sig string
			_ = json.Unmarshal(body["Sign"], &sig)
			var ts string
			_ = json.Unmarshal(body["Timestamp"], &ts)
			digest := sha256.Sum256([]byte(`GameID=730&HashName=["P250 | Re.built (Field-Tested)"]&PartnerId=test&Timestamp=` + ts))
			decoded, _ := base64.StdEncoding.DecodeString(sig)
			if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], decoded); err != nil {
				t.Fatal(err)
			}
			io.WriteString(w, `{"ResultCode":0,"ResultData":[{"HashName":"P250 | Re.built (Field-Tested)","MinPrice":"0.50","SellCount":"7"}]}`)
		} else if r.URL.Path == buyPath {
			if string(body["Price"]) != "0.5" || string(body["MaxCount"]) != "1" {
				t.Fatal("ceiling not preserved")
			}
			io.WriteString(w, `{"ResultCode":0,"ResultData":[{"OrderNo":"123","MerchantNo":"test","PaymentPrice":0.51}]}`)
		} else {
			t.Fatal(r.URL.Path)
		}
	}))
	defer server.Close()
	c, err := NewClient("test", keyData, "https://steamcommunity.com/tradeoffer/new/?partner=1&token=test", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	c.baseURL = server.URL
	q, err := c.Quote(context.Background(), hash)
	if err != nil || q != 50 {
		t.Fatal(q, err)
	}
	if _, err := c.Buy(context.Background(), hash, "test", q); err == nil {
		t.Fatal("accepted payment above confirmed quote")
	}
	if calls != 2 {
		t.Fatal("unexpected retry")
	}
}
