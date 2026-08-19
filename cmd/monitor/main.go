package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/app"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

const (
	smisBaseURL   = "https://smis.club"
	defaultDBPath = "./steam-knife-monitor.db"
	httpTimeout   = 30 * time.Second
)

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
	dbPath := flag.String("db", defaultDBPath, "SQLite database path")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stateStore, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open state store: %w", err)
	}
	defer func() {
		if err := stateStore.Close(); err != nil {
			log.Printf("close state store: %v", err)
		}
	}()

	httpClient := &http.Client{Timeout: httpTimeout}
	client := collector.NewSMISClient(smisBaseURL, httpClient)
	log.Printf("monitor started: database=%s", *dbPath)
	if err := app.Run(ctx, client, stateStore, defaultRunConfig); err != nil {
		return err
	}
	log.Printf("monitor stopped")
	return nil
}
