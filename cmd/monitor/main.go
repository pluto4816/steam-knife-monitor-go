package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/app"
	"github.com/pluto4816/steam-knife-monitor-go/internal/c5"
	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

const (
	smisBaseURL              = "https://smis.club"
	steamCommunityBaseURL    = "https://steamcommunity.com"
	c5BaseURL                = "https://openapi.c5game.com"
	byMykelDefinitionsURL    = "https://raw.githubusercontent.com/ByMykel/CSGO-API/main/public/api/en/skins_not_grouped.json"
	defaultDBPath            = "./steam-knife-monitor.db"
	defaultCatalogPath       = "./steam-candidates.json"
	httpTimeout              = 30 * time.Second
	candidateSnapshotTimeout = 2 * time.Minute

	modeMonitor          executionMode = "monitor"
	modeOnce             executionMode = "once"
	modePreview          executionMode = "preview"
	modeUpdateCandidates executionMode = "update-candidates"
	modePurchaseTest     executionMode = "purchase-test"
	modeECOTest          executionMode = "eco-purchase-test"
	modeSales            executionMode = "sales"
)

type executionMode string

type commandConfig struct {
	Mode          executionMode
	DBPath        string
	CandidatePath string
	NotifyEmpty   bool
	C5Purchase    app.C5PurchaseMode
	C5ItemID      int64
	ECOPurchase   app.ECOPurchaseMode
	SalesListen   string
	SalesOrigin   string
	SalesCert     string
	SalesKey      string
}

var defaultRunConfig = app.RunConfig{
	FullScanInterval: 24 * time.Hour,
	DuePollInterval:  60 * time.Second,
	PageDelay:        2 * time.Second,
	RequestDelay:     1 * time.Second,
	ErrorDelay:       5 * time.Minute,
}

func main() {
	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := parseCommandConfig(os.Args[1:])
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if config.Mode == modeSales {
		return runSales(ctx, config)
	}

	httpClient := &http.Client{Timeout: httpTimeout}
	if config.Mode == modeECOTest {
		return runECOTest(ctx, config, httpClient)
	}
	smisClient := collector.NewSMISClient(smisBaseURL, httpClient)
	if config.Mode == modeUpdateCandidates {
		log.Printf("candidate update started: catalog=%s", config.CandidatePath)
		definitionsClient := collector.NewCS2DefinitionsClient(
			byMykelDefinitionsURL,
			&http.Client{Timeout: candidateSnapshotTimeout},
		)
		result, err := app.RunCandidateUpdate(
			ctx,
			definitionsClient,
			smisClient,
			config.CandidatePath,
			defaultRunConfig.PageDelay,
			defaultRunConfig.RequestDelay,
		)
		logCandidateUpdateSummary(result)
		if err != nil {
			return err
		}
		log.Printf("candidate update complete: catalog=%s", config.CandidatePath)
		return nil
	}

	candidateCatalog, err := catalog.Load(config.CandidatePath)
	if err != nil {
		return fmt.Errorf(
			"load candidate catalog %s; run -mode update-candidates first: %w",
			config.CandidatePath,
			err,
		)
	}
	runtimeConfig := defaultRunConfig
	runtimeConfig.Candidates = candidateCatalog.Items
	var steamPrices app.SteamPriceSource
	if (config.C5Purchase != app.C5PurchaseOff || config.ECOPurchase != app.ECOPurchaseOff) && config.Mode != modePurchaseTest {
		steamPrices = collector.NewSteamPriceClient(steamCommunityBaseURL, httpClient)
	}

	sender, err := senderForMode(config.Mode, httpClient)
	if err != nil {
		return err
	}

	dbPath := config.DBPath
	if config.Mode == modePreview {
		dbPath = ":memory:"
	}
	stateStore, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open state store: %w", err)
	}
	defer func() {
		if err := stateStore.Close(); err != nil {
			log.Printf("close state store: %v", err)
		}
	}()

	if config.Mode == modeMonitor {
		resume, err := app.EvaluateTargetedScanResume(
			ctx,
			stateStore,
			candidateCatalog.Items,
			runtimeConfig.FullScanInterval,
			time.Now().UTC(),
		)
		if err != nil {
			return fmt.Errorf("evaluate targeted scan resume: %w", err)
		}
		if resume.Eligible {
			runtimeConfig.InitialFullScanCompletedAt = resume.CompletedAt
			log.Printf(
				"initial full scan skipped: reason=%s completed_at=%s candidates=%d catalog_fingerprint=%s",
				resume.Reason,
				resume.CompletedAt.UTC().Format(time.RFC3339),
				len(candidateCatalog.Items),
				resume.CatalogFingerprint,
			)
		} else {
			log.Printf("initial full scan required: reason=%s", resume.Reason)
		}
	}

	var purchaseRunner app.PurchaseRunner
	if config.Mode == modePurchaseTest {
		purchaseRunner, err = newManualC5PurchaseTestRunner(
			config, stateStore, candidateCatalog.Items, httpClient,
		)
	} else if config.ECOPurchase != app.ECOPurchaseOff {
		purchaseRunner, err = newECOPurchaseRunner(config, smisClient, steamPrices, stateStore, candidateCatalog.Items, httpClient)
	} else {
		purchaseRunner, err = newC5PurchaseRunner(
			config, smisClient, steamPrices, stateStore, candidateCatalog.Items, httpClient,
		)
	}
	if err != nil {
		return err
	}
	runtimeConfig.Purchase = purchaseRunner
	if config.ECOPurchase != app.ECOPurchaseOff {
		log.Printf("ECO purchase configured: mode=%s per_order_limit=1.00 daily_limit=10.00 timezone=Asia/Shanghai", config.ECOPurchase)
	}

	log.Printf(
		"candidate catalog loaded: path=%s generated_at=%s candidates=%d",
		config.CandidatePath,
		candidateCatalog.GeneratedAt.UTC().Format(time.RFC3339),
		len(candidateCatalog.Items),
	)
	if config.Mode == modePurchaseTest {
		log.Printf(
			"manual C5 purchase test configured: item_id=%d max_price=%s ledger_kind=%s",
			config.C5ItemID,
			app.C5PerOrderLimit,
			store.C5PurchaseManualTest,
		)
	} else if config.C5Purchase != app.C5PurchaseOff {
		log.Printf(
			"C5 purchase configured: mode=%s steam_price_source=SteamCommunityMarket per_order_limit=%s daily_limit=%s timezone=Asia/Shanghai",
			config.C5Purchase,
			app.C5PerOrderLimit,
			app.C5DailyLimit,
		)
	}
	switch config.Mode {
	case modeMonitor:
		log.Printf("monitor started: database=%s", config.DBPath)
		if err := app.Run(ctx, smisClient, stateStore, runtimeConfig, sender); err != nil {
			return err
		}
		log.Printf("monitor stopped")
	case modeOnce:
		log.Printf("one-time scan started: database=%s", config.DBPath)
		result, err := app.RunOneShot(ctx, smisClient, stateStore, runtimeConfig, sender)
		logOneShotSummary("one-time scan", result)
		if err != nil {
			return err
		}
		if config.NotifyEmpty {
			sent, err := app.SendNoSuitableItemsIfNeeded(ctx, sender, result.TierCounts)
			if err != nil {
				return err
			}
			if sent {
				log.Printf("one-time scan no-suitable-items notification sent")
			}
		}
		log.Printf("one-time scan complete")
	case modePreview:
		log.Printf("preview started: database=in-memory fresh_state=true qq_delivery=disabled")
		result, err := app.RunOneShot(ctx, smisClient, stateStore, runtimeConfig, nil)
		logOneShotSummary("preview", result)
		if err != nil {
			return err
		}
		alerts, err := stateStore.ListPendingAlerts(ctx)
		if err != nil {
			return fmt.Errorf("list preview alerts: %w", err)
		}
		for _, alert := range alerts {
			log.Printf(
				"preview would send: alert_id=%d item_id=%d kind=%s\n%s",
				alert.ID,
				alert.ItemID,
				alert.Kind,
				alert.Message,
			)
		}
		log.Printf("preview complete: fresh_state=true would_send=%d", len(alerts))
	case modePurchaseTest:
		log.Printf(
			"manual C5 purchase test started: database=%s item_id=%d max_price=%s",
			config.DBPath,
			config.C5ItemID,
			app.C5PerOrderLimit,
		)
		result, err := purchaseRunner.Run(ctx)
		log.Printf(
			"manual C5 purchase test summary: reconciled=%d confirmed=%d placed=%d",
			result.ReconciledCount,
			result.ConfirmedCount,
			result.PlacedCount,
		)
		if err != nil {
			return err
		}
		if result.PlacedPurchase == nil {
			return fmt.Errorf("manual C5 purchase test completed without a placed purchase")
		}
		if err := app.SendC5PurchaseReceipt(ctx, sender, *result.PlacedPurchase); err != nil {
			return err
		}
		log.Printf("manual C5 purchase test complete")
	default:
		return fmt.Errorf("unsupported mode %q", config.Mode)
	}
	return nil
}

func parseCommandConfig(args []string) (commandConfig, error) {
	config := commandConfig{
		Mode:          modeMonitor,
		DBPath:        defaultDBPath,
		CandidatePath: defaultCatalogPath,
		C5Purchase:    app.C5PurchaseOff,
		ECOPurchase:   app.ECOPurchaseOff,
	}
	flags := flag.NewFlagSet("monitor", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mode := string(config.Mode)
	flags.StringVar(&mode, "mode", mode, "execution mode: monitor, once, preview, update-candidates, purchase-test, eco-purchase-test, or sales")
	flags.StringVar(&config.SalesListen, "sales-listen", "", "sales server address (default 127.0.0.1:8080)")
	flags.StringVar(&config.SalesOrigin, "sales-origin", "", "exact browser origin, e.g. https://sales.example.com")
	flags.StringVar(&config.SalesCert, "sales-tls-cert", "", "TLS certificate PEM file")
	flags.StringVar(&config.SalesKey, "sales-tls-key", "", "TLS private key PEM file")
	flags.StringVar(&config.DBPath, "db", config.DBPath, "SQLite database path")
	flags.StringVar(&config.CandidatePath, "candidates", config.CandidatePath, "candidate catalog JSON path")
	flags.BoolVar(&config.NotifyEmpty, "notify-empty", false, "send a QQ message after a successful one-time scan with no A-tier candidates")
	ecoPurchase := string(config.ECOPurchase)
	flags.StringVar(&ecoPurchase, "eco-purchase", ecoPurchase, "ECO purchase mode: off, dry-run, or live")
	c5Purchase := string(config.C5Purchase)
	flags.StringVar(&c5Purchase, "c5-purchase", c5Purchase, "C5 purchase mode: off, dry-run, or live")
	flags.Int64Var(&config.C5ItemID, "c5-item-id", 0, "catalog item ID for purchase-test mode")
	if err := flags.Parse(args); err != nil {
		return commandConfig{}, err
	}
	if flags.NArg() != 0 {
		return commandConfig{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	config.Mode = executionMode(mode)
	switch config.Mode {
	case modeMonitor, modeOnce, modePreview, modeUpdateCandidates, modePurchaseTest, modeECOTest, modeSales:
	default:
		return commandConfig{}, fmt.Errorf(
			"invalid mode %q: want monitor, once, preview, update-candidates, purchase-test, eco-purchase-test, or sales",
			mode,
		)
	}
	config.C5Purchase = app.C5PurchaseMode(c5Purchase)
	config.ECOPurchase = app.ECOPurchaseMode(ecoPurchase)
	if config.ECOPurchase != app.ECOPurchaseOff && config.ECOPurchase != app.ECOPurchaseDryRun && config.ECOPurchase != app.ECOPurchaseLive {
		return commandConfig{}, fmt.Errorf("invalid ECO purchase mode")
	}
	if config.ECOPurchase != app.ECOPurchaseOff {
		if config.Mode != modeMonitor && config.Mode != modeOnce {
			return commandConfig{}, fmt.Errorf("-eco-purchase requires -mode monitor or once")
		}
		if config.C5Purchase != app.C5PurchaseOff {
			return commandConfig{}, fmt.Errorf("only one purchase platform may be enabled")
		}
		if config.DBPath == "" || strings.ContainsAny(config.DBPath, ":?\x00") {
			return commandConfig{}, fmt.Errorf("ECO requires a durable SQLite file path")
		}
	}
	if config.Mode == modeSales {
		if config.C5Purchase != app.C5PurchaseOff || config.ECOPurchase != app.ECOPurchaseOff {
			return commandConfig{}, fmt.Errorf("sales mode requires purchasing off")
		}
	} else if config.SalesListen != "" || config.SalesOrigin != "" || config.SalesCert != "" || config.SalesKey != "" {
		return commandConfig{}, fmt.Errorf("sales web flags require -mode sales")
	}
	if config.Mode == modeECOTest && config.C5Purchase != app.C5PurchaseOff {
		return commandConfig{}, fmt.Errorf("eco-purchase-test requires -c5-purchase off")
	}
	switch config.C5Purchase {
	case app.C5PurchaseOff, app.C5PurchaseDryRun, app.C5PurchaseLive:
	default:
		return commandConfig{}, fmt.Errorf(
			"invalid C5 purchase mode %q: want off, dry-run, or live",
			c5Purchase,
		)
	}
	if config.C5Purchase != app.C5PurchaseOff &&
		(config.Mode == modePreview || config.Mode == modeUpdateCandidates) {
		return commandConfig{}, fmt.Errorf("-c5-purchase requires -mode monitor, once, or purchase-test")
	}
	if config.Mode == modePurchaseTest {
		if config.C5Purchase != app.C5PurchaseLive {
			return commandConfig{}, fmt.Errorf("-mode purchase-test requires -c5-purchase live")
		}
		if config.C5ItemID <= 0 {
			return commandConfig{}, fmt.Errorf("-mode purchase-test requires a positive -c5-item-id")
		}
	} else if config.C5ItemID != 0 {
		return commandConfig{}, fmt.Errorf("-c5-item-id requires -mode purchase-test")
	}
	if config.NotifyEmpty && config.Mode != modeOnce {
		return commandConfig{}, fmt.Errorf("-notify-empty requires -mode once")
	}
	return config, nil
}

func senderForMode(mode executionMode, httpClient *http.Client) (app.Sender, error) {
	if mode == modePreview || mode == modeUpdateCandidates || mode == modeECOTest || mode == modeSales {
		return nil, nil
	}
	return newQQSenderFromEnv(httpClient)
}

func logCandidateUpdateSummary(result app.CandidateUpdateResult) {
	log.Printf(
		"candidate update summary: definition_records=%d definition_candidates=%d definition_sha256=%s smis=%d matched=%d reused=%d details_fetched=%d definition_only=%d smis_only=%d",
		result.DefinitionRecordCount,
		result.DefinitionCandidateCount,
		result.DefinitionSnapshotSHA256,
		result.SMISCandidateCount,
		result.MatchedCount,
		result.ReusedCount,
		result.DetailFetchedCount,
		result.DefinitionOnlyCount,
		result.SMISOnlyCount,
	)
	for _, name := range result.DefinitionOnlyNames {
		log.Printf("candidate update omitted definition-only item: market_hash_name=%q", name)
	}
}

func logOneShotSummary(label string, result app.OneShotResult) {
	log.Printf(
		"%s summary: candidates=%d resumed=%d matched=%d processed=%d duplicates=%d due=%d due_processed=%d tiers=A:%d,B1:%d,B2:%d,B3:%d,C:%d purchase_reconciled=%d purchase_confirmed=%d purchase_excluded=%d purchase_dry_run=%d purchase_placed=%d purchase_rejected=%d notifications_pending=%d notifications_sent=%d",
		label,
		result.Scan.CandidateCount,
		result.Scan.ResumedCount,
		result.Scan.MatchedCount,
		result.Scan.ProcessedCount,
		result.Scan.DuplicateCount,
		result.DueChecks.DueCount,
		result.DueChecks.ProcessedCount,
		result.TierCounts.A,
		result.TierCounts.B1,
		result.TierCounts.B2,
		result.TierCounts.B3,
		result.TierCounts.C,
		result.Purchases.ReconciledCount,
		result.Purchases.ConfirmedCount,
		result.Purchases.ExcludedCount,
		result.Purchases.DryRunCount,
		result.Purchases.PlacedCount,
		result.Purchases.RejectedCount,
		result.Notifications.PendingCount,
		result.Notifications.SentCount,
	)
}

func newC5PurchaseRunner(
	config commandConfig,
	smisClient *collector.SMISClient,
	steamPrices app.SteamPriceSource,
	stateStore *store.Store,
	candidates []catalog.Item,
	httpClient *http.Client,
) (app.PurchaseRunner, error) {
	switch config.C5Purchase {
	case app.C5PurchaseOff:
		return nil, nil
	case app.C5PurchaseDryRun:
		runner, err := app.NewC5PurchaseService(
			config.C5Purchase, smisClient, steamPrices, stateStore, nil, candidates,
			defaultRunConfig.RequestDelay,
		)
		if err != nil {
			return nil, fmt.Errorf("configure C5 dry-run purchase service: %w", err)
		}
		return runner, nil
	case app.C5PurchaseLive:
		client, err := c5.NewClient(
			c5BaseURL,
			os.Getenv("C5_APP_KEY"),
			os.Getenv("C5_TRADE_URL"),
			httpClient,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"configure C5 live purchase from C5_APP_KEY and C5_TRADE_URL: %w",
				err,
			)
		}
		runner, err := app.NewC5PurchaseService(
			config.C5Purchase, smisClient, steamPrices, stateStore, client, candidates,
			defaultRunConfig.RequestDelay,
		)
		if err != nil {
			return nil, fmt.Errorf("configure C5 live purchase service: %w", err)
		}
		return runner, nil
	default:
		return nil, fmt.Errorf("unsupported C5 purchase mode %q", config.C5Purchase)
	}
}

func newManualC5PurchaseTestRunner(
	config commandConfig,
	stateStore *store.Store,
	candidates []catalog.Item,
	httpClient *http.Client,
) (app.PurchaseRunner, error) {
	var target catalog.Item
	for _, candidate := range candidates {
		if candidate.ItemID == config.C5ItemID {
			target = candidate
			break
		}
	}
	if target.ItemID == 0 {
		return nil, fmt.Errorf("manual C5 purchase test item %d is not in the candidate catalog", config.C5ItemID)
	}
	client, err := c5.NewClient(
		c5BaseURL,
		os.Getenv("C5_APP_KEY"),
		os.Getenv("C5_TRADE_URL"),
		httpClient,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"configure manual C5 purchase test from C5_APP_KEY and C5_TRADE_URL: %w",
			err,
		)
	}
	runner, err := app.NewManualC5PurchaseTestService(
		stateStore,
		client,
		target,
		app.C5PerOrderLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("configure manual C5 purchase test service: %w", err)
	}
	return runner, nil
}

func newQQSenderFromEnv(httpClient *http.Client) (*app.QQBotSender, error) {
	sender, err := app.NewQQBotSender(
		os.Getenv("QQ_BOT_APP_ID"),
		os.Getenv("QQ_BOT_APP_SECRET"),
		os.Getenv("QQ_BOT_USER_OPENID"),
		httpClient,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"configure QQ sender from QQ_BOT_APP_ID, QQ_BOT_APP_SECRET, and QQ_BOT_USER_OPENID: %w",
			err,
		)
	}
	return sender, nil
}
