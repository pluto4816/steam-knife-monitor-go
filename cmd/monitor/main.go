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
	"syscall"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/app"
	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

const (
	smisBaseURL              = "https://smis.club"
	byMykelDefinitionsURL    = "https://raw.githubusercontent.com/ByMykel/CSGO-API/main/public/api/en/skins_not_grouped.json"
	defaultDBPath            = "./steam-knife-monitor.db"
	defaultCatalogPath       = "./steam-candidates.json"
	httpTimeout              = 30 * time.Second
	candidateSnapshotTimeout = 2 * time.Minute

	modeMonitor          executionMode = "monitor"
	modeOnce             executionMode = "once"
	modePreview          executionMode = "preview"
	modeUpdateCandidates executionMode = "update-candidates"
)

type executionMode string

type commandConfig struct {
	Mode          executionMode
	DBPath        string
	CandidatePath string
	NotifyEmpty   bool
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

	httpClient := &http.Client{Timeout: httpTimeout}
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

	log.Printf(
		"candidate catalog loaded: path=%s generated_at=%s candidates=%d",
		config.CandidatePath,
		candidateCatalog.GeneratedAt.UTC().Format(time.RFC3339),
		len(candidateCatalog.Items),
	)
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
	}
	flags := flag.NewFlagSet("monitor", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mode := string(config.Mode)
	flags.StringVar(&mode, "mode", mode, "execution mode: monitor, once, preview, or update-candidates")
	flags.StringVar(&config.DBPath, "db", config.DBPath, "SQLite database path")
	flags.StringVar(&config.CandidatePath, "candidates", config.CandidatePath, "candidate catalog JSON path")
	flags.BoolVar(&config.NotifyEmpty, "notify-empty", false, "send a QQ message after a successful one-time scan with no A-tier candidates")
	if err := flags.Parse(args); err != nil {
		return commandConfig{}, err
	}
	if flags.NArg() != 0 {
		return commandConfig{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	config.Mode = executionMode(mode)
	switch config.Mode {
	case modeMonitor, modeOnce, modePreview, modeUpdateCandidates:
	default:
		return commandConfig{}, fmt.Errorf(
			"invalid mode %q: want monitor, once, preview, or update-candidates",
			mode,
		)
	}
	if config.NotifyEmpty && config.Mode != modeOnce {
		return commandConfig{}, fmt.Errorf("-notify-empty requires -mode once")
	}
	return config, nil
}

func senderForMode(mode executionMode, httpClient *http.Client) (app.Sender, error) {
	if mode == modePreview || mode == modeUpdateCandidates {
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
		"%s summary: candidates=%d resumed=%d matched=%d processed=%d duplicates=%d due=%d due_processed=%d tiers=A:%d,B1:%d,B2:%d,B3:%d,C:%d notifications_pending=%d notifications_sent=%d",
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
		result.Notifications.PendingCount,
		result.Notifications.SentCount,
	)
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
