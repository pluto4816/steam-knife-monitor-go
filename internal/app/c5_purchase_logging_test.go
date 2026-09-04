package app

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"testing"
)

func TestC5PurchaseDryRunLogsExclusionReasons(t *testing.T) {
	stateStore := openTestStore(t)
	for itemID := int64(1); itemID <= 3; itemID++ {
		saveC5AState(t, stateStore, itemID)
	}
	client, requests := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		switch path {
		case "/api/commodity/1":
			return http.StatusOK, c5DetailData(1, "1.15", "0.61", 330)
		case "/api/commodity/2":
			body := c5DetailData(2, "2.30", "0.99", 400)
			return http.StatusOK, strings.Replace(
				body,
				"\"buffSellPrice\":2",
				"\"buffSellPrice\":0.80",
				1,
			)
		case "/api/commodity/3":
			return http.StatusOK, c5DetailData(3, "2.30", "1.01", 400)
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	steamPrices := &fakeSteamPriceSource{prices: map[string]string{
		"hash-1": "1.15",
		"hash-2": "2.30",
		"hash-3": "2.30",
	}}
	service, err := NewC5PurchaseService(
		C5PurchaseDryRun,
		client,
		steamPrices,
		stateStore,
		nil,
		c5Candidates(1, 2, 3),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result != (PurchaseRunResult{ExcludedCount: 3}) {
		t.Fatalf("result = %#v", result)
	}
	for itemID := int64(1); itemID <= 3; itemID++ {
		if requests.countPath(fmt.Sprintf("/api/commodity/%d", itemID)) != 1 {
			t.Fatalf("item %d request count is not one: %#v", itemID, requests.snapshot())
		}
	}
	if fmt.Sprint(steamPrices.calls) != "[hash-1]" {
		t.Fatalf("Steam calls = %#v, want only the candidate that passed C5 prefilter", steamPrices.calls)
	}
	logs := output.String()
	for _, fragment := range []string{
		`item_id=1 market_hash_name="hash-1" reason=not_base_a_after_steam_reprice`,
		`refreshed_tier=B1 ratio_bp=`,
		`steam_daily_volume=330`,
		`item_id=2 market_hash_name="hash-2" reason=lowest_platform_not_c5`,
		`lowest_platform="BUFF" lowest_platform_price="0.80"`,
		`item_id=3 market_hash_name="hash-3" reason=above_per_order_limit`,
		`c5_price=1.01 per_order_limit=1.00`,
	} {
		if !strings.Contains(logs, fragment) {
			t.Fatalf("logs do not contain %q:\n%s", fragment, logs)
		}
	}
}
