package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestParseCommandConfigDefaultsToMonitor(t *testing.T) {
	config, err := parseCommandConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if config != (commandConfig{Mode: modeMonitor, DBPath: defaultDBPath, CandidatePath: defaultCatalogPath}) {
		t.Fatalf("config = %#v", config)
	}
}

func TestParseCommandConfigAcceptsModesAndPaths(t *testing.T) {
	for _, mode := range []executionMode{modeOnce, modePreview, modeUpdateCandidates} {
		t.Run(string(mode), func(t *testing.T) {
			config, err := parseCommandConfig([]string{
				"-mode", string(mode),
				"-db", "custom.db",
				"-candidates", "custom.json",
			})
			if err != nil {
				t.Fatal(err)
			}
			if config.Mode != mode || config.DBPath != "custom.db" || config.CandidatePath != "custom.json" {
				t.Fatalf("config = %#v", config)
			}
		})
	}
}

func TestParseCommandConfigAcceptsNotifyEmptyOnlyForOnce(t *testing.T) {
	config, err := parseCommandConfig([]string{"-mode", "once", "-notify-empty"})
	if err != nil {
		t.Fatal(err)
	}
	if !config.NotifyEmpty {
		t.Fatalf("config = %#v, want notify-empty enabled", config)
	}

	for _, mode := range []executionMode{modeMonitor, modePreview, modeUpdateCandidates} {
		t.Run(string(mode), func(t *testing.T) {
			_, err := parseCommandConfig([]string{"-mode", string(mode), "-notify-empty"})
			if err == nil || !strings.Contains(err.Error(), "requires -mode once") {
				t.Fatalf("error = %v, want once-only rejection", err)
			}
		})
	}
}

func TestParseCommandConfigRejectsUnknownModeAndArguments(t *testing.T) {
	if _, err := parseCommandConfig([]string{"-mode", "worker"}); err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("unknown mode error = %v", err)
	}
	if _, err := parseCommandConfig([]string{"extra"}); err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("unexpected argument error = %v", err)
	}
}

func TestNonProductionModesDoNotRequireQQCredentials(t *testing.T) {
	t.Setenv("QQ_BOT_APP_ID", "")
	t.Setenv("QQ_BOT_APP_SECRET", "")
	t.Setenv("QQ_BOT_USER_OPENID", "")

	for _, mode := range []executionMode{modePreview, modeUpdateCandidates} {
		sender, err := senderForMode(mode, &http.Client{})
		if err != nil || sender != nil {
			t.Fatalf("mode %s sender = %#v, err = %v", mode, sender, err)
		}
	}
	if _, err := senderForMode(modeOnce, &http.Client{}); err == nil {
		t.Fatal("one-time production mode accepted missing QQ credentials")
	}
}
