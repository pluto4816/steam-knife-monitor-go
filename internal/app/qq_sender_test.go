package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

func TestNewQQBotSenderValidatesRequiredValues(t *testing.T) {
	client := &http.Client{}
	tests := []struct {
		name       string
		appID      string
		secret     string
		userOpenID string
		client     *http.Client
	}{
		{name: "empty app ID", secret: "secret", userOpenID: "openid", client: client},
		{name: "empty secret", appID: "appid", userOpenID: "openid", client: client},
		{name: "empty openid", appID: "appid", secret: "secret", client: client},
		{name: "nil client", appID: "appid", secret: "secret", userOpenID: "openid"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewQQBotSender(testCase.appID, testCase.secret, testCase.userOpenID, testCase.client); err == nil {
				t.Fatal("NewQQBotSender accepted invalid configuration")
			}
		})
	}
}

func TestQQBotSenderSendsPersistedMessageSnapshot(t *testing.T) {
	const (
		appID      = "test-app-id"
		secret     = "test-secret"
		userOpenID = "test-user-openid"
		token      = "test-access-token"
		message    = "persisted trigger snapshot"
	)
	tokenCalls := 0
	messageCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/token":
			tokenCalls++
			if request.Method != http.MethodPost {
				t.Errorf("token method = %s, want POST", request.Method)
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode token body: %v", err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			if len(body) != 2 || jsonString(body["appId"]) != appID || jsonString(body["clientSecret"]) != secret {
				t.Error("token request did not contain exactly the configured appId and clientSecret")
			}
			_, _ = fmt.Fprintf(response, `{"access_token":%q,"expires_in":7200}`, token)
		case "/v2/users/" + userOpenID + "/messages":
			messageCalls++
			if request.Method != http.MethodPost {
				t.Errorf("message method = %s, want POST", request.Method)
			}
			if got := request.Header.Get("Authorization"); got != "QQBot "+token {
				t.Errorf("Authorization = %q, want fake QQBot token", got)
			}
			if got := request.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode message body: %v", err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			if len(body) != 2 || jsonString(body["content"]) != message || string(body["msg_type"]) != "0" {
				t.Errorf("message body = %s, want only content snapshot and msg_type=0", mustJSON(body))
			}
			for _, forbidden := range []string{"msg_id", "event_id", "msg_seq", "is_wakeup"} {
				if _, found := body[forbidden]; found {
					t.Errorf("message body unexpectedly contains %s", forbidden)
				}
			}
			_, _ = fmt.Fprint(response, `{"id":"message-id","timestamp":123}`)
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(response, `{}`)
		}
	}))
	defer server.Close()

	sender, err := newQQBotSender(appID, secret, userOpenID, server.Client(), server.URL+"/token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), store.Alert{Message: message}); err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 1 || messageCalls != 1 {
		t.Fatalf("calls token=%d message=%d, want 1 and 1", tokenCalls, messageCalls)
	}
}

func TestQQBotSenderCachesAccessToken(t *testing.T) {
	tokenCalls := 0
	messageCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/token" {
			tokenCalls++
			_, _ = fmt.Fprint(response, `{"access_token":"cached-token","expires_in":"7200"}`)
			return
		}
		if request.URL.Path == "/v2/users/openid/messages" {
			messageCalls++
			_, _ = fmt.Fprintf(response, `{"id":"message-%d"}`, messageCalls)
			return
		}
		response.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(response, `{}`)
	}))
	defer server.Close()

	sender := newTestQQBotSender(t, server)
	for _, message := range []string{"first snapshot", "second snapshot"} {
		if err := sender.Send(context.Background(), store.Alert{Message: message}); err != nil {
			t.Fatal(err)
		}
	}
	if tokenCalls != 1 || messageCalls != 2 {
		t.Fatalf("calls token=%d message=%d, want 1 and 2", tokenCalls, messageCalls)
	}
	if sender.tokenExpiry.Before(time.Now().Add(time.Hour)) {
		t.Fatalf("quoted expires_in did not set a future expiry: %s", sender.tokenExpiry)
	}
}

func TestQQBotSenderRefreshesTokenWithinSafetyWindow(t *testing.T) {
	tokenCalls := 0
	messageCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/token" {
			tokenCalls++
			_, _ = fmt.Fprintf(response, `{"access_token":"token-%d","expires_in":7200}`, tokenCalls)
			return
		}
		if request.URL.Path == "/v2/users/openid/messages" {
			messageCalls++
			_, _ = fmt.Fprintf(response, `{"id":"message-%d"}`, messageCalls)
			return
		}
		response.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(response, `{}`)
	}))
	defer server.Close()

	sender := newTestQQBotSender(t, server)
	if err := sender.Send(context.Background(), store.Alert{Message: "first"}); err != nil {
		t.Fatal(err)
	}
	sender.tokenExpiry = time.Now().Add(tokenRefreshGap)
	if err := sender.Send(context.Background(), store.Alert{Message: "second"}); err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 2 || messageCalls != 2 {
		t.Fatalf("calls token=%d message=%d, want 2 and 2", tokenCalls, messageCalls)
	}
}

func TestQQBotSenderTokenFailureDoesNotSendOrLeakSecret(t *testing.T) {
	const secret = "fake-secret-must-not-leak"
	tokenCalls := 0
	messageCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/token" {
			tokenCalls++
			response.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprintf(response, `{"code":100016,"message":"invalid %s"}`, secret)
			return
		}
		messageCalls++
		response.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(response, `{}`)
	}))
	defer server.Close()

	sender, err := newQQBotSender("appid", secret, "openid", server.Client(), server.URL+"/token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	err = sender.Send(context.Background(), store.Alert{Message: "snapshot"})
	if err == nil || !strings.Contains(err.Error(), "100016") {
		t.Fatalf("token error = %v, want QQ code 100016", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("token error leaked app secret")
	}
	if tokenCalls != 1 {
		t.Fatalf("token calls = %d, want exactly 1", tokenCalls)
	}
	if messageCalls != 0 {
		t.Fatalf("message calls = %d, want 0", messageCalls)
	}
}

func TestQQBotSenderMessageFailureDoesNotLeakToken(t *testing.T) {
	const token = "fake-token-must-not-leak"
	messageCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/token" {
			_, _ = fmt.Fprintf(response, `{"access_token":%q,"expires_in":7200}`, token)
			return
		}
		messageCalls++
		response.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprintf(response, `{"code":22009,"msg":"msg limit exceed %s"}`, token)
	}))
	defer server.Close()

	sender := newTestQQBotSender(t, server)
	err := sender.Send(context.Background(), store.Alert{Message: "snapshot"})
	if err == nil || !strings.Contains(err.Error(), "22009") || !strings.Contains(err.Error(), "msg limit exceed") {
		t.Fatalf("message error = %v, want rate-limit details", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("message error leaked access token")
	}
	if messageCalls != 1 {
		t.Fatalf("message calls = %d, want exactly 1", messageCalls)
	}
}

func TestQQBotSenderDoesNotFollowRedirects(t *testing.T) {
	const (
		secret = "fake-secret-must-not-leak"
		token  = "fake-token-must-not-leak"
	)
	tests := []struct {
		name       string
		redirectOn string
	}{
		{name: "access token", redirectOn: "/token"},
		{name: "C2C message", redirectOn: "/v2/users/openid/messages"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			redirectedCalls := 0
			redirected := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				redirectedCalls++
				response.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(response, `{}`)
			}))
			defer redirected.Close()

			tokenCalls := 0
			messageCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/token":
					tokenCalls++
					if testCase.redirectOn == request.URL.Path {
						response.Header().Set("Location", redirected.URL+"/"+secret)
						response.WriteHeader(http.StatusTemporaryRedirect)
						_, _ = fmt.Fprint(response, `{"code":307,"message":"redirect"}`)
						return
					}
					_, _ = fmt.Fprintf(response, `{"access_token":%q,"expires_in":7200}`, token)
				case "/v2/users/openid/messages":
					messageCalls++
					response.Header().Set("Location", redirected.URL+"/"+token)
					response.WriteHeader(http.StatusTemporaryRedirect)
					_, _ = fmt.Fprint(response, `{"code":307,"message":"redirect"}`)
				default:
					response.WriteHeader(http.StatusNotFound)
					_, _ = fmt.Fprint(response, `{}`)
				}
			}))
			defer server.Close()

			sender, err := newQQBotSender("appid", secret, "openid", server.Client(), server.URL+"/token", server.URL)
			if err != nil {
				t.Fatal(err)
			}
			err = sender.Send(context.Background(), store.Alert{Message: "snapshot"})
			if err == nil {
				t.Fatal("redirect response was accepted")
			}
			if redirectedCalls != 0 {
				t.Fatalf("redirect target calls = %d, want 0", redirectedCalls)
			}
			wantMessageCalls := 0
			if testCase.redirectOn != "/token" {
				wantMessageCalls = 1
			}
			if tokenCalls != 1 || messageCalls != wantMessageCalls {
				t.Fatalf("calls token=%d message=%d, want 1 and %d", tokenCalls, messageCalls, wantMessageCalls)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), token) {
				t.Fatal("redirect error leaked a QQ credential")
			}
		})
	}
}

func TestQQBotSenderRedactsTransportError(t *testing.T) {
	const secret = "fake-secret-must-not-leak"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("transport echoed %s", secret)
	})}
	sender, err := NewQQBotSender("appid", secret, "openid", client)
	if err != nil {
		t.Fatal(err)
	}
	err = sender.Send(context.Background(), store.Alert{Message: "snapshot"})
	if err == nil {
		t.Fatal("transport error was accepted")
	}
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("transport error was not redacted: %v", err)
	}
}

func TestQQBotSenderRejectsInvalidTokenResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"access_token":`},
		{name: "empty token", body: `{"access_token":"","expires_in":7200}`},
		{name: "missing expiry", body: `{"access_token":"token"}`},
		{name: "invalid expiry", body: `{"access_token":"token","expires_in":0}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			messageCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/token" {
					_, _ = fmt.Fprint(response, testCase.body)
					return
				}
				messageCalls++
				_, _ = fmt.Fprint(response, `{"id":"unexpected"}`)
			}))
			defer server.Close()

			sender := newTestQQBotSender(t, server)
			if err := sender.Send(context.Background(), store.Alert{Message: "snapshot"}); err == nil {
				t.Fatal("invalid token response was accepted")
			}
			if messageCalls != 0 {
				t.Fatalf("message calls = %d, want 0", messageCalls)
			}
		})
	}
}

func TestQQBotSenderRejectsMalformedMessageResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/token" {
			_, _ = fmt.Fprint(response, `{"access_token":"token","expires_in":7200}`)
			return
		}
		_, _ = fmt.Fprint(response, `{"id":`)
	}))
	defer server.Close()

	sender := newTestQQBotSender(t, server)
	if err := sender.Send(context.Background(), store.Alert{Message: "snapshot"}); err == nil {
		t.Fatal("malformed message response was accepted")
	}
}

func TestQQBotSenderAcceptsSuccessfulResponseWithoutMessageID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/token" {
			_, _ = fmt.Fprint(response, `{"access_token":"token","expires_in":7200}`)
			return
		}
		_, _ = fmt.Fprint(response, `{}`)
	}))
	defer server.Close()

	sender := newTestQQBotSender(t, server)
	if err := sender.Send(context.Background(), store.Alert{Message: "snapshot"}); err != nil {
		t.Fatalf("successful response without message ID was rejected: %v", err)
	}
}

func newTestQQBotSender(t *testing.T, server *httptest.Server) *QQBotSender {
	t.Helper()
	sender, err := newQQBotSender("appid", "secret", "openid", server.Client(), server.URL+"/token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return sender
}

func jsonString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func mustJSON(value interface{}) string {
	data, _ := json.Marshal(value)
	return string(data)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
