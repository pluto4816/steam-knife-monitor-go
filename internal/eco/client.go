package eco

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const BaseURL = "https://openapi.ecosteam.cn"
const TestHashName = "P250 | Re.built (Field-Tested)"
const TestNameCN = "P250 | 重构 (久经沙场)"
const PerOrderLimit MoneyFen = 100

const balancePath = "/Api/Merchant/GetTotalMoney"
const buyPath = "/Api/open/buy/BuyByHashName"
const quotePath = "/Api/Market/BatchSearchSellingPrice"
const orderPath = "/Api/open/order/OrderDetailsInfo"

type Client struct {
	baseURL   string
	http      *http.Client
	partnerID string
	key       *rsa.PrivateKey
	tradeLink string
	now       func() time.Time
}

// NewClient uses only ECO's official host. Redirects never forward signed credentials.
func NewClient(partnerID string, privateKey []byte, tradeLink string, httpClient *http.Client) (*Client, error) {
	if strings.TrimSpace(partnerID) == "" {
		return nil, errors.New("ECO_PARTNER_ID is required")
	}
	u, err := url.Parse(tradeLink)
	if err != nil || u.Scheme != "https" || u.Host != "steamcommunity.com" || u.Path != "/tradeoffer/new/" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("ECO_TRADE_URL must be a Steam HTTPS trade-offer link")
	}
	partner, err := strconv.ParseUint(u.Query().Get("partner"), 10, 64)
	if err != nil || partner == 0 || u.Query().Get("token") == "" {
		return nil, errors.New("ECO_TRADE_URL requires partner and token")
	}
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *httpClient
	if copyClient.Timeout <= 0 {
		copyClient.Timeout = 30 * time.Second
	}
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{baseURL: BaseURL, http: &copyClient, partnerID: partnerID, key: key, tradeLink: tradeLink, now: time.Now}, nil
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	bad := errors.New("ECO private key must be a valid RSA private key of at least 2048 bits (PEM or base64 DER)")
	der := data
	if block, rest := pem.Decode(data); block != nil {
		if len(bytes.TrimSpace(rest)) != 0 {
			return nil, bad
		}
		der = block.Bytes
	} else {
		var err error
		der, err = base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(data)), ""))
		if err != nil {
			return nil, bad
		}
	}
	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		v, e := x509.ParsePKCS8PrivateKey(der)
		if e != nil {
			return nil, bad
		}
		var ok bool
		key, ok = v.(*rsa.PrivateKey)
		if !ok {
			return nil, bad
		}
	}
	if key.N.BitLen() < 2048 || key.Validate() != nil {
		return nil, bad
	}
	return key, nil
}

// Signing supports the scalar fields and hash-name array used by the documented endpoints.
// Sort ignoring case, as in ECO's official Python signing example. Do not URL-encode values.
func signFields(fields map[string]any, key *rsa.PrivateKey) (string, error) {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if strings.EqualFold(k, "sign") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.ToLower(keys[i]) < strings.ToLower(keys[j]) })
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		var value string
		switch v := fields[k].(type) {
		case string:
			value = v
		case json.Number:
			value = v.String()
		case []string:
			var buffer bytes.Buffer
			encoder := json.NewEncoder(&buffer)
			encoder.SetEscapeHTML(false)
			err := encoder.Encode(v)
			if err != nil {
				return "", errors.New("encode ECO signing array failed")
			}
			value = strings.TrimSuffix(buffer.String(), "\n")
		default:
			return "", errors.New("unsupported ECO signing value")
		}
		parts = append(parts, k+"="+value)
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "&")))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", errors.New("ECO signing failed")
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

type APIError struct{ Code int }

func (e *APIError) Error() string {
	return fmt.Sprintf("ECO API failed (ResultCode=%d); inspect the official account for details", e.Code)
}

// Never return raw bodies, upstream messages or request errors: these can echo credentials.
func (c *Client) post(ctx context.Context, path string, fields map[string]any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fields["PartnerId"] = c.partnerID
	fields["Timestamp"] = strconv.FormatInt(c.now().Unix(), 10)
	sig, err := signFields(fields, c.key)
	if err != nil {
		return err
	}
	fields["Sign"] = sig
	body, err := json.Marshal(fields)
	if err != nil {
		return errors.New("encode ECO request failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("create ECO request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("ECO transport failed; request is not retried")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ECO HTTP status %d; request is not retried", resp.StatusCode)
	}
	const maxBody = 2 << 20
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(b) > maxBody || len(bytes.TrimSpace(b)) == 0 {
		return errors.New("ECO response empty, oversized or unreadable")
	}
	var envelope struct {
		Code json.RawMessage `json:"ResultCode"`
		Data json.RawMessage `json:"ResultData"`
	}
	if json.Unmarshal(b, &envelope) != nil {
		return errors.New("invalid ECO response JSON")
	}
	codeText := string(envelope.Code)
	if len(codeText) > 0 && codeText[0] == '"' {
		if json.Unmarshal(envelope.Code, &codeText) != nil {
			return errors.New("invalid ECO result code")
		}
	}
	code, err := strconv.Atoi(codeText)
	if err != nil {
		return errors.New("missing or invalid ECO result code")
	}
	if code != 0 {
		return &APIError{Code: code}
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) || json.Unmarshal(envelope.Data, out) != nil {
		return errors.New("invalid ECO result data")
	}
	return nil
}

func (c *Client) Balance(ctx context.Context) (MoneyFen, error) {
	var data struct {
		Money     json.RawMessage
		LockMoney json.RawMessage
	}
	if err := c.post(ctx, balancePath, map[string]any{}, &data); err != nil {
		return 0, err
	}
	money, err := decodeMoney(data.Money)
	if err != nil {
		return 0, err
	}
	locked, err := decodeMoney(data.LockMoney)
	if err != nil || locked > money {
		return 0, errors.New("invalid ECO wallet/frozen amounts")
	}
	// Conservatively exclude frozen money until the live representation is verified.
	return money - locked, nil
}

type BuyResult struct {
	OrderNo    string
	MerchantNo string
	Paid       MoneyFen
}

func (c *Client) BuyTestItem(ctx context.Context, merchantNo string) (BuyResult, error) {
	return c.Buy(ctx, TestHashName, merchantNo, PerOrderLimit)
}

func (c *Client) Buy(ctx context.Context, hashName, merchantNo string, ceiling MoneyFen) (BuyResult, error) {
	if strings.TrimSpace(hashName) == "" || ceiling <= 0 || ceiling > PerOrderLimit {
		return BuyResult{}, errors.New("invalid ECO purchase identity or ceiling")
	}
	if merchantNo == "" || len(merchantNo) > 64 {
		return BuyResult{}, errors.New("invalid ECO merchant number")
	}
	var data []struct {
		OrderNo      string
		MerchantNo   string
		PaymentPrice json.RawMessage
		OrderIdList  []string
	}
	err := c.post(ctx, buyPath, map[string]any{
		// Use an integer JSON token for the exact one-yuan ceiling so JSON
		// number normalization cannot change the signed text (1.00 versus 1).
		"GameId": "730", "HashName": hashName, "Price": json.Number(strings.TrimRight(strings.TrimRight(ceiling.String(), "0"), ".")),
		"TradeLink": c.tradeLink, "MaxCount": json.Number("1"), "MerchantNo": merchantNo,
	}, &data)
	if err != nil {
		return BuyResult{}, err
	}
	if len(data) != 1 || !validOrderNo(data[0].OrderNo) || data[0].MerchantNo != merchantNo || len(data[0].OrderIdList) != 0 {
		return BuyResult{}, errors.New("ambiguous ECO purchase result")
	}
	paid, err := decodeMoney(data[0].PaymentPrice)
	if err != nil || paid <= 0 || paid > ceiling {
		return BuyResult{}, errors.New("ECO payment absent, invalid or exceeds CNY 1.00")
	}
	return BuyResult{OrderNo: data[0].OrderNo, MerchantNo: merchantNo, Paid: paid}, nil
}

type Order struct {
	OrderNo    string
	MerchantNo string
	HashName   string
	Paid       MoneyFen
	State      int
}

func (c *Client) Order(ctx context.Context, merchantNo string) (Order, error) {
	return c.OrderForItem(ctx, merchantNo, TestHashName, PerOrderLimit)
}

func (c *Client) OrderForItem(ctx context.Context, merchantNo, hashName string, ceiling MoneyFen) (Order, error) {
	if strings.TrimSpace(hashName) == "" || ceiling <= 0 || ceiling > PerOrderLimit {
		return Order{}, errors.New("invalid ECO reconciliation identity or ceiling")
	}
	if merchantNo == "" || len(merchantNo) > 64 {
		return Order{}, errors.New("invalid ECO merchant number")
	}
	var data struct {
		OrderNum       string
		MerchantNo     string
		HashName       string
		TotalMoney     json.RawMessage
		BuyerFee       json.RawMessage
		OrderStateCode *int
	}
	if err := c.post(ctx, orderPath, map[string]any{"GameId": "730", "MerchantNo": merchantNo}, &data); err != nil {
		return Order{}, err
	}
	if !validOrderNo(data.OrderNum) || data.MerchantNo != merchantNo || data.HashName != hashName || data.OrderStateCode == nil {
		return Order{}, errors.New("ECO order identity or state mismatch")
	}
	paid, err := decodeMoney(data.TotalMoney)
	if err != nil || paid <= 0 || paid > ceiling {
		return Order{}, errors.New("invalid ECO order amount")
	}
	fee, err := decodeMoney(data.BuyerFee)
	if err != nil || paid+fee > ceiling {
		return Order{}, errors.New("ECO order plus fee exceeds limit or fee is invalid")
	}
	return Order{OrderNo: data.OrderNum, MerchantNo: merchantNo, HashName: data.HashName, Paid: paid + fee, State: *data.OrderStateCode}, nil
}

func validOrderNo(s string) bool {
	if s == "" || len(s) > 64 || strings.Trim(s, "0") == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Quote requests exactly one name; it never crawls the ECO catalog.
func (c *Client) Quote(ctx context.Context, hashName string) (MoneyFen, error) {
	if strings.TrimSpace(hashName) == "" {
		return 0, errors.New("missing ECO quote identity")
	}
	var rows []struct {
		HashName  string
		MinPrice  json.RawMessage
		SellCount json.RawMessage
	}
	if err := c.post(ctx, quotePath, map[string]any{"GameID": "730", "HashName": []string{hashName}}, &rows); err != nil {
		return 0, err
	}
	if len(rows) != 1 || rows[0].HashName != hashName {
		return 0, errors.New("missing or mismatched ECO quote")
	}
	count := string(rows[0].SellCount)
	if len(count) > 0 && count[0] == '"' {
		if json.Unmarshal(rows[0].SellCount, &count) != nil {
			return 0, errors.New("invalid ECO availability")
		}
	}
	n, err := strconv.ParseUint(count, 10, 64)
	if err != nil {
		return 0, errors.New("invalid ECO availability")
	}
	if n == 0 {
		return 0, nil
	}
	price, err := decodeMoney(rows[0].MinPrice)
	if err != nil || price <= 0 {
		return 0, errors.New("invalid ECO quote price")
	}
	return price, nil
}
