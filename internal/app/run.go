package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type RunConfig struct {
	FullScanInterval time.Duration
	DuePollInterval  time.Duration
	PageDelay        time.Duration
	RequestDelay     time.Duration
	ErrorDelay       time.Duration

	InitialFullScanCompletedAt time.Time

	// Candidates is nil only for the frozen V1 scan tests. Production modes load
	// a non-empty persisted catalog and therefore always use targeted details.
	Candidates []catalog.Item

	Purchase PurchaseRunner
}

func Run(
	ctx context.Context,
	client *collector.SMISClient,
	stateStore *store.Store,
	config RunConfig,
	sender Sender,
) error {
	if err := validateRunConfig(config); err != nil {
		return err
	}

	var nextFullScanAttempt time.Time
	resumePurchasePending := false
	if !config.InitialFullScanCompletedAt.IsZero() {
		nextFullScanAttempt = config.InitialFullScanCompletedAt.UTC().Add(config.FullScanInterval)
		resumePurchasePending = true
	}
	var nextNotificationAttempt time.Time
	var nextPurchaseAttempt time.Time
	for {
		if ctx.Err() != nil {
			return nil
		}

		purchaseNeeded := resumePurchasePending
		if nextFullScanAttempt.IsZero() || !time.Now().UTC().Before(nextFullScanAttempt) {
			result, err := runConfiguredScan(ctx, client, stateStore, config)
			completedAt := time.Now().UTC()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				if errors.Is(err, collector.ErrAccessRestricted) {
					return fmt.Errorf("full scan access restricted: %w", err)
				}
				log.Printf(
					"full scan error: candidates=%d resumed=%d matched=%d processed=%d duplicates=%d: %v",
					result.CandidateCount,
					result.ResumedCount,
					result.MatchedCount,
					result.ProcessedCount,
					result.DuplicateCount,
					err,
				)
				nextFullScanAttempt = completedAt.Add(config.ErrorDelay)
				if config.Candidates != nil {
					if err := waitContext(ctx, config.ErrorDelay); err != nil {
						return nil
					}
					continue
				}
			} else {
				log.Printf(
					"full scan complete: candidates=%d resumed=%d matched=%d processed=%d duplicates=%d",
					result.CandidateCount,
					result.ResumedCount,
					result.MatchedCount,
					result.ProcessedCount,
					result.DuplicateCount,
				)
				nextFullScanAttempt = completedAt.Add(config.FullScanInterval)
				purchaseNeeded = true
			}

			if err := waitContext(ctx, config.RequestDelay); err != nil {
				return nil
			}
		}

		result, dueErr := runConfiguredDueChecks(ctx, client, stateStore, config)
		nextLoopDelay := config.DuePollInterval
		if dueErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(dueErr, collector.ErrAccessRestricted) {
				return fmt.Errorf("due checks access restricted: %w", dueErr)
			}
			log.Printf(
				"due checks error: due=%d processed=%d: %v",
				result.DueCount,
				result.ProcessedCount,
				dueErr,
			)
			nextLoopDelay = config.ErrorDelay
		} else if result.DueCount > 0 {
			log.Printf(
				"due checks complete: due=%d processed=%d",
				result.DueCount,
				result.ProcessedCount,
			)
		}

		now := time.Now().UTC()
		if dueErr == nil && config.Purchase != nil &&
			(purchaseNeeded || result.ProcessedCount > 0 || !nextPurchaseAttempt.IsZero()) &&
			(nextPurchaseAttempt.IsZero() || !now.Before(nextPurchaseAttempt)) {
			purchaseResult, err := config.Purchase.Run(ctx)
			resumePurchasePending = false
			completedAt := time.Now().UTC()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				log.Printf(
					"purchase phase error: reconciled=%d confirmed=%d excluded=%d dry_run=%d placed=%d rejected=%d: %v",
					purchaseResult.ReconciledCount,
					purchaseResult.ConfirmedCount,
					purchaseResult.ExcludedCount,
					purchaseResult.DryRunCount,
					purchaseResult.PlacedCount,
					purchaseResult.RejectedCount,
					err,
				)
				nextPurchaseAttempt = completedAt.Add(config.ErrorDelay)
			} else {
				logPurchaseRunResult("purchase phase complete", purchaseResult)
				nextPurchaseAttempt = time.Time{}
				if purchaseResult.PlacedPurchase != nil || purchaseResult.ECOPlacedPurchase != nil {
					if err := sendPurchaseResult(ctx, sender, purchaseResult); err != nil {
						return err
					}
					log.Printf("purchase receipt sent; monitor stopping after first purchase")
					return nil
				}
			}
		}

		now = time.Now().UTC()
		if sender != nil && (nextNotificationAttempt.IsZero() || !now.Before(nextNotificationAttempt)) {
			sendResult, err := SendPendingAlerts(ctx, stateStore, sender)
			completedAt := time.Now().UTC()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				log.Printf(
					"notification send error: pending=%d sent=%d: %v",
					sendResult.PendingCount,
					sendResult.SentCount,
					err,
				)
				nextNotificationAttempt = completedAt.Add(config.ErrorDelay)
			} else {
				if sendResult.PendingCount > 0 {
					log.Printf(
						"notification send complete: pending=%d sent=%d",
						sendResult.PendingCount,
						sendResult.SentCount,
					)
				}
				nextNotificationAttempt = time.Time{}
			}
		}

		if err := waitContext(ctx, nextLoopDelay); err != nil {
			return nil
		}
	}
}

func runConfiguredScan(
	ctx context.Context,
	client *collector.SMISClient,
	stateStore *store.Store,
	config RunConfig,
) (ScanResult, error) {
	if config.Candidates != nil {
		return RunTargetedScanCycle(
			ctx, client, stateStore, config.Candidates, config.RequestDelay,
		)
	}
	return RunScanCycle(ctx, client, stateStore, config.PageDelay)
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
	if !config.InitialFullScanCompletedAt.IsZero() && config.Candidates == nil {
		return fmt.Errorf("initial full scan completion requires targeted candidates")
	}
	if config.Candidates != nil && len(config.Candidates) == 0 {
		return fmt.Errorf("candidate catalog cannot be empty")
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

func logPurchaseRunResult(label string, result PurchaseRunResult) {
	if result.ReconciledCount == 0 &&
		result.ConfirmedCount == 0 &&
		result.ExcludedCount == 0 &&
		result.DryRunCount == 0 &&
		result.PlacedCount == 0 &&
		result.RejectedCount == 0 {
		return
	}
	log.Printf(
		"%s: reconciled=%d confirmed=%d excluded=%d dry_run=%d placed=%d rejected=%d",
		label, result.ReconciledCount, result.ConfirmedCount, result.ExcludedCount, result.DryRunCount,
		result.PlacedCount, result.RejectedCount,
	)
}
