package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
)

type TargetedScanCheckpoint struct {
	CatalogFingerprint string
	CompletedCount     int
	LastItemID         int64
}

func (store *Store) GetTargetedScanCheckpoint(ctx context.Context) (TargetedScanCheckpoint, bool, error) {
	var checkpoint TargetedScanCheckpoint
	var completedCount int64
	err := store.db.QueryRowContext(ctx, `
SELECT catalog_fingerprint, completed_count, last_item_id
FROM targeted_scan_checkpoint
WHERE singleton = 1`).Scan(
		&checkpoint.CatalogFingerprint,
		&completedCount,
		&checkpoint.LastItemID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return TargetedScanCheckpoint{}, false, nil
	}
	if err != nil {
		return TargetedScanCheckpoint{}, false, fmt.Errorf("get targeted scan checkpoint: %w", err)
	}
	if completedCount <= 0 || (strconv.IntSize == 32 && completedCount > 1<<31-1) {
		return TargetedScanCheckpoint{}, false, fmt.Errorf(
			"stored targeted scan checkpoint has invalid completed count %d",
			completedCount,
		)
	}
	checkpoint.CompletedCount = int(completedCount)
	if err := validateTargetedScanCheckpoint(checkpoint); err != nil {
		return TargetedScanCheckpoint{}, false, fmt.Errorf("stored targeted scan checkpoint is invalid: %w", err)
	}
	return checkpoint, true, nil
}

func (store *Store) SaveTargetedScanCheckpoint(ctx context.Context, checkpoint TargetedScanCheckpoint) error {
	if err := validateTargetedScanCheckpoint(checkpoint); err != nil {
		return err
	}
	_, err := store.db.ExecContext(ctx, `
INSERT INTO targeted_scan_checkpoint (
    singleton, catalog_fingerprint, completed_count, last_item_id
) VALUES (1, ?, ?, ?)
ON CONFLICT(singleton) DO UPDATE SET
    catalog_fingerprint = excluded.catalog_fingerprint,
    completed_count = excluded.completed_count,
    last_item_id = excluded.last_item_id`,
		checkpoint.CatalogFingerprint,
		checkpoint.CompletedCount,
		checkpoint.LastItemID,
	)
	if err != nil {
		return fmt.Errorf("save targeted scan checkpoint: %w", err)
	}
	return nil
}

func (store *Store) ClearTargetedScanCheckpoint(ctx context.Context, catalogFingerprint string) error {
	if err := validateCatalogFingerprint(catalogFingerprint); err != nil {
		return err
	}
	if _, err := store.db.ExecContext(ctx, `
DELETE FROM targeted_scan_checkpoint
WHERE singleton = 1 AND catalog_fingerprint = ?`, catalogFingerprint); err != nil {
		return fmt.Errorf("clear targeted scan checkpoint: %w", err)
	}
	return nil
}

func validateTargetedScanCheckpoint(checkpoint TargetedScanCheckpoint) error {
	if err := validateCatalogFingerprint(checkpoint.CatalogFingerprint); err != nil {
		return err
	}
	if checkpoint.CompletedCount <= 0 {
		return fmt.Errorf("targeted scan checkpoint completed count must be positive")
	}
	if checkpoint.LastItemID <= 0 {
		return fmt.Errorf("targeted scan checkpoint last item ID must be positive")
	}
	return nil
}

func validateCatalogFingerprint(fingerprint string) error {
	if len(fingerprint) != 64 {
		return fmt.Errorf("catalog fingerprint must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(fingerprint); err != nil {
		return fmt.Errorf("catalog fingerprint must be hexadecimal")
	}
	return nil
}
