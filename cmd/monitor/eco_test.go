package main

import (
	"context"
	"net/http"
	"testing"
)

func TestECOModeIsIsolated(t *testing.T) {
	config, err := parseCommandConfig([]string{"-mode", "eco-purchase-test"})
	if err != nil || config.Mode != modeECOTest {
		t.Fatal(config, err)
	}
	for _, args := range [][]string{
		{"-mode", "eco-purchase-test", "-c5-purchase", "live"},
		{"-mode", "eco-purchase-test", "-notify-empty"},
		{"-mode", "eco-purchase-test", "-c5-item-id", "1"},
	} {
		if _, err := parseCommandConfig(args); err == nil {
			t.Fatal("accepted", args)
		}
	}
	if sender, err := senderForMode(modeECOTest, &http.Client{}); err != nil || sender != nil {
		t.Fatal("ECO constructed QQ sender")
	}
	t.Setenv("ECO_PARTNER_ID", "")
	t.Setenv("ECO_PRIVATE_KEY_FILE", "")
	t.Setenv("ECO_TRADE_URL", "")
	if _, err := ecoClientFromEnv(nil); err == nil {
		t.Fatal("accepted absent credentials")
	}
	for _, path := range []string{":memory:", "file:test?mode=memory", ""} {
		if err := runECOTest(context.Background(), commandConfig{DBPath: path}, nil); err == nil {
			t.Fatal("accepted nondurable ledger")
		}
	}
}

func TestAutomaticECOFlags(t *testing.T) {
	for _, mode := range []string{"monitor", "once"} {
		for _, purchase := range []string{"off", "dry-run", "live"} {
			c, err := parseCommandConfig([]string{"-mode", mode, "-eco-purchase", purchase})
			if err != nil || string(c.ECOPurchase) != purchase {
				t.Fatal(c, err)
			}
		}
	}
	for _, args := range [][]string{
		{"-eco-purchase", "unknown"},
		{"-eco-purchase", "live", "-c5-purchase", "live"},
		{"-eco-purchase", "dry-run", "-mode", "preview"},
		{"-eco-purchase", "live", "-mode", "update-candidates"},
		{"-eco-purchase", "live", "-mode", "eco-purchase-test"},
		{"-eco-purchase", "live", "-db", ":memory:"},
	} {
		if _, err := parseCommandConfig(args); err == nil {
			t.Fatal("accepted", args)
		}
	}
}
