package main

import (
	"net/http"
	"testing"
)

func TestSalesModeIsolation(t *testing.T) {
	c, e := parseCommandConfig([]string{"-mode", "sales", "-sales-origin", "https://sales.example.com"})
	if e != nil || c.Mode != modeSales {
		t.Fatal(c, e)
	}
	for _, args := range [][]string{
		{"-mode", "sales", "-c5-purchase", "live"},
		{"-mode", "sales", "-eco-purchase", "dry-run"},
		{"-mode", "sales", "-notify-empty"},
		{"-mode", "sales", "-c5-item-id", "1"},
		{"-mode", "once", "-sales-listen", "127.0.0.1:8080"},
	} {
		if _, e := parseCommandConfig(args); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	t.Setenv("QQ_BOT_APP_ID", "")
	s, e := senderForMode(modeSales, &http.Client{})
	if e != nil || s != nil {
		t.Fatal("sales requires QQ")
	}
}
