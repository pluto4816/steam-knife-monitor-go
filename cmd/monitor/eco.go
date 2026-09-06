package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/pluto4816/steam-knife-monitor-go/internal/app"
	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/eco"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

func runECOTest(ctx context.Context, config commandConfig, httpClient *http.Client) error {
	// An in-memory or URI database could discard the durable reservation on exit.
	if config.DBPath == "" || strings.Contains(config.DBPath, ":") || strings.ContainsAny(config.DBPath, "?\x00") {
		return errors.New("ECO test requires a durable SQLite file path, not memory or URI")
	}
	client, err := ecoClientFromEnv(httpClient)
	if err != nil {
		return err
	}
	s, err := store.Open(config.DBPath)
	if err != nil {
		return fmt.Errorf("open ECO ledger: %w", err)
	}
	defer s.Close()
	log.Printf("ECO manual test: item=%q quantity=1 max_price=1.00 daily_limit=10.00", eco.TestNameCN)
	result, err := app.RunManualECOTest(ctx, s, client)
	if result.Order.OrderNo != "" {
		log.Printf("ECO order recorded: order_no=%s paid=%s order_state=%d submitted=%t reconciled=%t (order creation is not Steam receipt confirmation)", result.Order.OrderNo, result.Order.Paid, result.Order.State, result.Submitted, result.Reconciled)
	}
	return err
}

func ecoClientFromEnv(httpClient *http.Client) (*eco.Client, error) {
	id := os.Getenv("ECO_PARTNER_ID")
	keyPath := os.Getenv("ECO_PRIVATE_KEY_FILE")
	tradeURL := os.Getenv("ECO_TRADE_URL")
	if id == "" || keyPath == "" || tradeURL == "" {
		return nil, errors.New("ECO_PARTNER_ID, ECO_PRIVATE_KEY_FILE and ECO_TRADE_URL are required; do not put credentials on the command line")
	}
	info, err := os.Stat(keyPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<10 {
		return nil, errors.New("ECO private key file is missing, not regular, or oversized")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, errors.New("cannot read ECO private key file")
	}
	return eco.NewClient(id, key, tradeURL, httpClient)
}

func newECOPurchaseRunner(config commandConfig, smis *collector.SMISClient, steam app.SteamPriceSource, s *store.Store, candidates []catalog.Item, httpClient *http.Client) (app.PurchaseRunner, error) {
	client, err := ecoClientFromEnv(httpClient)
	if err != nil {
		return nil, err
	}
	return app.NewECOPurchaseService(config.ECOPurchase, smis, steam, s, client, candidates, defaultRunConfig.RequestDelay)
}
