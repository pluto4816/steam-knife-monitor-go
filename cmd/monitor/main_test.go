package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestNewQQSenderFromEnvRequiresEveryCredential(t *testing.T) {
	tests := []struct {
		name    string
		missing string
	}{
		{name: "app ID", missing: "QQ_BOT_APP_ID"},
		{name: "app secret", missing: "QQ_BOT_APP_SECRET"},
		{name: "user openid", missing: "QQ_BOT_USER_OPENID"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("QQ_BOT_APP_ID", "app-id")
			t.Setenv("QQ_BOT_APP_SECRET", "app-secret")
			t.Setenv("QQ_BOT_USER_OPENID", "user-openid")
			t.Setenv(testCase.missing, "")

			_, err := newQQSenderFromEnv(&http.Client{})
			if err == nil {
				t.Fatal("missing QQ credential was accepted")
			}
			if !strings.Contains(err.Error(), testCase.missing) {
				t.Fatalf("error = %q, want missing environment variable name", err)
			}
		})
	}
}

func TestNewQQSenderFromEnvAcceptsCompleteConfiguration(t *testing.T) {
	t.Setenv("QQ_BOT_APP_ID", "app-id")
	t.Setenv("QQ_BOT_APP_SECRET", "app-secret")
	t.Setenv("QQ_BOT_USER_OPENID", "user-openid")

	sender, err := newQQSenderFromEnv(&http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if sender == nil {
		t.Fatal("complete QQ configuration returned nil sender")
	}
}
