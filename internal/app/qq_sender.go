package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

const (
	qqTokenURL      = "https://api.bot.qq.com/app/getAppAccessToken"
	qqAPIBaseURL    = "https://api.bot.qq.com"
	tokenRefreshGap = 60 * time.Second
	maxQQResponse   = 1 << 20
)

type QQBotSender struct {
	appID      string
	appSecret  string
	userOpenID string
	httpClient *http.Client

	accessToken string
	tokenExpiry time.Time

	tokenURL   string
	apiBaseURL string
}

var _ Sender = (*QQBotSender)(nil)

func NewQQBotSender(
	appID string,
	appSecret string,
	userOpenID string,
	httpClient *http.Client,
) (*QQBotSender, error) {
	return newQQBotSender(appID, appSecret, userOpenID, httpClient, qqTokenURL, qqAPIBaseURL)
}

func newQQBotSender(
	appID string,
	appSecret string,
	userOpenID string,
	httpClient *http.Client,
	tokenURL string,
	apiBaseURL string,
) (*QQBotSender, error) {
	if appID == "" {
		return nil, fmt.Errorf("QQ bot app ID cannot be empty")
	}
	if appSecret == "" {
		return nil, fmt.Errorf("QQ bot app secret cannot be empty")
	}
	if userOpenID == "" {
		return nil, fmt.Errorf("QQ bot user openid cannot be empty")
	}
	if httpClient == nil {
		return nil, fmt.Errorf("QQ bot HTTP client cannot be nil")
	}
	if tokenURL == "" || apiBaseURL == "" {
		return nil, fmt.Errorf("QQ bot API endpoints cannot be empty")
	}
	qqHTTPClient := *httpClient
	qqHTTPClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &QQBotSender{
		appID:      appID,
		appSecret:  appSecret,
		userOpenID: userOpenID,
		httpClient: &qqHTTPClient,
		tokenURL:   tokenURL,
		apiBaseURL: strings.TrimRight(apiBaseURL, "/"),
	}, nil
}

func (sender *QQBotSender) Send(ctx context.Context, alert store.Alert) error {
	if err := sender.ensureAccessToken(ctx); err != nil {
		return err
	}

	body, err := json.Marshal(struct {
		Content string `json:"content"`
		MsgType int    `json:"msg_type"`
	}{
		Content: alert.Message,
		MsgType: 0,
	})
	if err != nil {
		return fmt.Errorf("encode QQ C2C message: %w", err)
	}
	endpoint := sender.apiBaseURL + "/v2/users/" + url.PathEscape(sender.userOpenID) + "/messages"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create QQ C2C message request: %w", err)
	}
	request.Header.Set("Authorization", "QQBot "+sender.accessToken)
	request.Header.Set("Content-Type", "application/json")

	response, err := sender.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send QQ C2C message: %s", sender.redactSecrets(err.Error()))
	}
	defer response.Body.Close()

	var result qqMessageResponse
	if err := decodeQQResponse(response.Body, &result); err != nil {
		return fmt.Errorf(
			"decode QQ C2C message response (HTTP %d): %s",
			response.StatusCode,
			sender.redactSecrets(err.Error()),
		)
	}
	if err := sender.responseError(response.StatusCode, result.Code, qqResponseMessage(result.Message, result.Msg)); err != nil {
		return fmt.Errorf("send QQ C2C message: %w", err)
	}
	return nil
}

func (sender *QQBotSender) ensureAccessToken(ctx context.Context) error {
	now := time.Now()
	if sender.accessToken != "" && now.Before(sender.tokenExpiry.Add(-tokenRefreshGap)) {
		return nil
	}

	body, err := json.Marshal(struct {
		AppID        string `json:"appId"`
		ClientSecret string `json:"clientSecret"`
	}{
		AppID:        sender.appID,
		ClientSecret: sender.appSecret,
	})
	if err != nil {
		return fmt.Errorf("encode QQ access token request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, sender.tokenURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create QQ access token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := sender.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request QQ access token: %s", sender.redactSecrets(err.Error()))
	}
	defer response.Body.Close()

	var result qqTokenResponse
	if err := decodeQQResponse(response.Body, &result); err != nil {
		return fmt.Errorf(
			"decode QQ access token response (HTTP %d): %s",
			response.StatusCode,
			sender.redactSecrets(err.Error()),
		)
	}
	if err := sender.responseError(response.StatusCode, result.Code, qqResponseMessage(result.Message, result.Msg)); err != nil {
		return fmt.Errorf("request QQ access token: %w", err)
	}
	if result.AccessToken == "" {
		return fmt.Errorf("request QQ access token: response token is empty")
	}
	ttl, err := parseQQExpiresIn(result.ExpiresIn)
	if err != nil {
		return fmt.Errorf("request QQ access token: %w", err)
	}
	sender.accessToken = result.AccessToken
	sender.tokenExpiry = time.Now().Add(ttl)
	return nil
}

type qqTokenResponse struct {
	AccessToken string          `json:"access_token"`
	ExpiresIn   json.RawMessage `json:"expires_in"`
	Code        json.RawMessage `json:"code"`
	Message     string          `json:"message"`
	Msg         string          `json:"msg"`
}

type qqMessageResponse struct {
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Msg     string          `json:"msg"`
}

func qqResponseMessage(message string, msg string) string {
	if message != "" {
		return message
	}
	return msg
}

func decodeQQResponse(body io.Reader, target interface{}) error {
	data, err := io.ReadAll(io.LimitReader(body, maxQQResponse))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func parseQQExpiresIn(raw json.RawMessage) (time.Duration, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return 0, fmt.Errorf("expires_in is missing")
	}
	if strings.HasPrefix(text, "\"") {
		var quoted string
		if err := json.Unmarshal(raw, &quoted); err != nil {
			return 0, fmt.Errorf("expires_in is invalid")
		}
		text = quoted
	}
	seconds, err := strconv.ParseInt(text, 10, 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("expires_in is not a positive integer")
	}
	ttl := time.Duration(seconds) * time.Second
	if ttl <= 0 || int64(ttl/time.Second) != seconds {
		return 0, fmt.Errorf("expires_in is too large")
	}
	return ttl, nil
}

func (sender *QQBotSender) responseError(status int, code json.RawMessage, message string) error {
	codeText := strings.Trim(strings.TrimSpace(string(code)), "\"")
	if status >= 200 && status < 300 && (codeText == "" || codeText == "null" || codeText == "0") {
		return nil
	}
	if codeText == "" || codeText == "null" {
		codeText = "-"
	}
	codeText = sender.redactSecrets(codeText)
	message = sender.redactSecrets(message)
	if message == "" {
		message = "-"
	}
	return fmt.Errorf("HTTP %d, QQ code=%s, message=%q", status, codeText, message)
}

func (sender *QQBotSender) redactSecrets(message string) string {
	for _, secret := range []string{sender.appSecret, sender.accessToken} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return message
}
