package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type RunConfig struct {
	FullScanInterval time.Duration
	DuePollInterval  time.Duration
	PageDelay        time.Duration
	RequestDelay     time.Duration
	ErrorDelay       time.Duration
}

func Run(
	ctx context.Context,
	client *collector.SMISClient,
	stateStore *store.Store,
	config RunConfig,
) error {
	if err := validateRunConfig(config); err != nil {
		return err
	}

	var nextFullScanAttempt time.Time
	for {
		if ctx.Err() != nil {
			return nil
		}

		if nextFullScanAttempt.IsZero() || !time.Now().UTC().Before(nextFullScanAttempt) {
			result, err := RunScanCycle(ctx, client, stateStore, config.PageDelay)
			completedAt := time.Now().UTC()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				if errors.Is(err, collector.ErrAccessRestricted) {
					return fmt.Errorf("full scan access restricted: %w", err)
				}
				log.Printf(
					"full scan error: candidates=%d matched=%d processed=%d duplicates=%d: %v",
					result.CandidateCount,
					result.MatchedCount,
					result.ProcessedCount,
					result.DuplicateCount,
					err,
				)
				nextFullScanAttempt = completedAt.Add(config.ErrorDelay)
			} else {
				log.Printf(
					"full scan complete: candidates=%d matched=%d processed=%d duplicates=%d",
					result.CandidateCount,
					result.MatchedCount,
					result.ProcessedCount,
					result.DuplicateCount,
				)
				nextFullScanAttempt = completedAt.Add(config.FullScanInterval)
			}

			if err := waitContext(ctx, config.RequestDelay); err != nil {
				return nil
			}
		}

		result, err := RunDueChecks(ctx, client, stateStore, config.RequestDelay)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, collector.ErrAccessRestricted) {
				return fmt.Errorf("due checks access restricted: %w", err)
			}
			log.Printf(
				"due checks error: due=%d processed=%d: %v",
				result.DueCount,
				result.ProcessedCount,
				err,
			)
			if err := waitContext(ctx, config.ErrorDelay); err != nil {
				return nil
			}
			continue
		}
		if result.DueCount > 0 {
			log.Printf(
				"due checks complete: due=%d processed=%d",
				result.DueCount,
				result.ProcessedCount,
			)
		}

		if err := waitContext(ctx, config.DuePollInterval); err != nil {
			return nil
		}
	}
}

func validateRunConfig(config RunConfig) error {
	if config.FullScanInterval <= 0 {
		return fmt.Errorf("full scan interval must be positive")
	}
	if config.DuePollInterval <= 0 {
		return fmt.Errorf("due poll interval must be positive")
	}
	if config.ErrorDelay <= 0 {
		return fmt.Errorf("error delay must be positive")
	}
	if config.PageDelay < 0 {
		return fmt.Errorf("page delay cannot be negative")
	}
	if config.RequestDelay < 0 {
		return fmt.Errorf("request delay cannot be negative")
	}
	return nil
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
