package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

type TargetedScanCompletion struct {
	CatalogFingerprint string
	CandidateCount     int
	CompletedAt        time.Time
}

func (store *Store) GetTargetedScanCompletion(ctx context.Context) (TargetedScanCompletion, bool, error) {
	var completion TargetedScanCompletion
	var candidateCount, completedAt int64
	err := store.db.QueryRowContext(ctx, `
SELECT catalog_fingerprint, candidate_count, completed_at
FROM targeted_scan_completion
WHERE singleton = 1`).Scan(
		&completion.CatalogFingerprint,
		&candidateCount,
		&completedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return TargetedScanCompletion{}, false, nil
	}
	if err != nil {
		return TargetedScanCompletion{}, false, fmt.Errorf("get targeted scan completion: %w", err)
	}
	if candidateCount <= 0 || (strconv.IntSize == 32 && candidateCount > 1<<31-1) {
		return TargetedScanCompletion{}, false, fmt.Errorf(
			"stored targeted scan completion has invalid candidate count %d",
			candidateCount,
		)
	}
	completion.CandidateCount = int(candidateCount)
	completion.CompletedAt = time.UnixMilli(completedAt).UTC()
	if err := validateTargetedScanCompletion(completion); err != nil {
		return TargetedScanCompletion{}, false, fmt.Errorf("stored targeted scan completion is invalid: %w", err)
	}
	return completion, true, nil
}

func (store *Store) SaveTargetedScanCompletion(ctx context.Context, completion TargetedScanCompletion) error {
	if err := validateTargetedScanCompletion(completion); err != nil {
		return err
	}
	_, err := store.db.ExecContext(ctx, `
INSERT INTO targeted_scan_completion (
    singleton, catalog_fingerprint, candidate_count, completed_at
) VALUES (1, ?, ?, ?)
ON CONFLICT(singleton) DO UPDATE SET
    catalog_fingerprint = excluded.catalog_fingerprint,
    candidate_count = excluded.candidate_count,
    completed_at = excluded.completed_at`,
		completion.CatalogFingerprint,
		completion.CandidateCount,
		completion.CompletedAt.UTC().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("save targeted scan completion: %w", err)
	}
	return nil
}

func validateTargetedScanCompletion(completion TargetedScanCompletion) error {
	if err := validateCatalogFingerprint(completion.CatalogFingerprint); err != nil {
		return err
	}
	if completion.CandidateCount <= 0 {
		return fmt.Errorf("targeted scan completion candidate count must be positive")
	}
	if completion.CompletedAt.IsZero() || completion.CompletedAt.UTC().UnixMilli() < 0 {
		return fmt.Errorf("targeted scan completion time must be on or after the Unix epoch")
	}
	return nil
}
