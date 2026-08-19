package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

type AlertStatus string

const (
	AlertStatusPending AlertStatus = "PENDING"
	AlertStatusSent    AlertStatus = "SENT"
)

type Alert struct {
	ID            int64
	ItemID        int64
	Kind          core.AlertKind
	AEntryAlertID *int64
	Ratio         core.RatioBP
	Message       string
	Status        AlertStatus
	CreatedAt     time.Time
	SentAt        *time.Time
}

type AlertDraft struct {
	ItemID        int64
	Kind          core.AlertKind
	AEntryAlertID *int64
	Ratio         core.RatioBP
	Message       string
	CreatedAt     time.Time
}

type AlertContext struct {
	AEntryAlertID int64
	HasEntry      bool
	HasPending    bool
	LastSentRatio core.RatioBP
	HasLastSent   bool
}

func (store *Store) SaveItemStateAndAlert(
	ctx context.Context,
	state ItemState,
	draft *AlertDraft,
) error {
	if err := validateState(state); err != nil {
		return err
	}
	if draft != nil {
		if err := validateAlertDraft(*draft); err != nil {
			return err
		}
		if draft.ItemID != state.ItemID {
			return fmt.Errorf("alert item ID %d does not match state item ID %d", draft.ItemID, state.ItemID)
		}
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin item state and alert transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, itemStateUpsert, itemStateArgs(state)...); err != nil {
		return fmt.Errorf("save item state %d: %w", state.ItemID, err)
	}
	if draft != nil {
		var aEntryAlertID interface{}
		if draft.AEntryAlertID != nil {
			aEntryAlertID = *draft.AEntryAlertID
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO alerts (
    item_id, kind, a_entry_alert_id, ratio_bp,
    message, status, created_at, sent_at
) VALUES (?, ?, ?, ?, ?, 'PENDING', ?, NULL)`,
			draft.ItemID,
			string(draft.Kind),
			aEntryAlertID,
			int64(draft.Ratio),
			draft.Message,
			draft.CreatedAt.UTC().UnixMilli(),
		); err != nil {
			return fmt.Errorf("create pending alert for item %d: %w", draft.ItemID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit item state and alert transaction: %w", err)
	}
	return nil
}

func (store *Store) GetAlertContext(ctx context.Context, itemID int64) (AlertContext, error) {
	if itemID <= 0 {
		return AlertContext{}, fmt.Errorf("item ID must be positive")
	}

	var alertContext AlertContext
	err := store.db.QueryRowContext(ctx, `
SELECT id
FROM alerts
WHERE item_id = ? AND kind = 'A_ENTRY'
ORDER BY id DESC
LIMIT 1`, itemID).Scan(&alertContext.AEntryAlertID)
	if errors.Is(err, sql.ErrNoRows) {
		return AlertContext{}, nil
	}
	if err != nil {
		return AlertContext{}, fmt.Errorf("get A-entry alert for item %d: %w", itemID, err)
	}
	alertContext.HasEntry = true

	var hasPending int
	var lastSentRatio sql.NullInt64
	err = store.db.QueryRowContext(ctx, `
SELECT
    EXISTS(
        SELECT 1
        FROM alerts
        WHERE (id = ? OR a_entry_alert_id = ?) AND status = 'PENDING'
    ),
    (
        SELECT ratio_bp
        FROM alerts
        WHERE (id = ? OR a_entry_alert_id = ?) AND status = 'SENT'
        ORDER BY sent_at DESC, id DESC
        LIMIT 1
    )`,
		alertContext.AEntryAlertID,
		alertContext.AEntryAlertID,
		alertContext.AEntryAlertID,
		alertContext.AEntryAlertID,
	).Scan(&hasPending, &lastSentRatio)
	if err != nil {
		return AlertContext{}, fmt.Errorf("get alert context for item %d: %w", itemID, err)
	}
	alertContext.HasPending = hasPending != 0
	if lastSentRatio.Valid {
		if lastSentRatio.Int64 < 0 || lastSentRatio.Int64 > maxRatioBP {
			return AlertContext{}, fmt.Errorf("stored alert for item %d has invalid ratio %d", itemID, lastSentRatio.Int64)
		}
		alertContext.LastSentRatio = core.RatioBP(lastSentRatio.Int64)
		alertContext.HasLastSent = true
	}
	return alertContext, nil
}

func (store *Store) ListPendingAlerts(ctx context.Context) ([]Alert, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT id, item_id, kind, a_entry_alert_id, ratio_bp,
       message, status, created_at, sent_at
FROM alerts
WHERE status = 'PENDING'
ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list pending alerts: %w", err)
	}
	defer rows.Close()

	alerts := []Alert{}
	for rows.Next() {
		var alert Alert
		var kind, status string
		var aEntryAlertID, sentAt sql.NullInt64
		var ratio, createdAt int64
		if err := rows.Scan(
			&alert.ID,
			&alert.ItemID,
			&kind,
			&aEntryAlertID,
			&ratio,
			&alert.Message,
			&status,
			&createdAt,
			&sentAt,
		); err != nil {
			return nil, fmt.Errorf("scan pending alert: %w", err)
		}
		alert.Kind = core.AlertKind(kind)
		alert.Status = AlertStatus(status)
		if ratio >= 0 && ratio <= maxRatioBP {
			alert.Ratio = core.RatioBP(ratio)
		}
		alert.CreatedAt = time.UnixMilli(createdAt).UTC()
		if aEntryAlertID.Valid {
			value := aEntryAlertID.Int64
			alert.AEntryAlertID = &value
		}
		if sentAt.Valid {
			value := time.UnixMilli(sentAt.Int64).UTC()
			alert.SentAt = &value
		}
		if ratio < 0 || ratio > maxRatioBP {
			return nil, fmt.Errorf("stored alert %d has invalid ratio %d", alert.ID, ratio)
		}
		if err := validateStoredAlert(alert); err != nil {
			return nil, fmt.Errorf("stored alert %d is invalid: %w", alert.ID, err)
		}
		alerts = append(alerts, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending alerts: %w", err)
	}
	return alerts, nil
}

func (store *Store) MarkAlertSent(ctx context.Context, alertID int64, sentAt time.Time) error {
	if alertID <= 0 {
		return fmt.Errorf("alert ID must be positive")
	}
	if sentAt.IsZero() {
		return fmt.Errorf("sent time cannot be zero")
	}
	sentAtMillis := sentAt.UTC().UnixMilli()
	if sentAtMillis < 0 {
		return fmt.Errorf("sent time cannot be before Unix epoch")
	}

	result, err := store.db.ExecContext(ctx, `
UPDATE alerts
SET status = 'SENT', sent_at = ?
WHERE id = ? AND status = 'PENDING'`, sentAtMillis, alertID)
	if err != nil {
		return fmt.Errorf("mark alert %d sent: %w", alertID, err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read mark-alert result for alert %d: %w", alertID, err)
	}
	if updated != 1 {
		return fmt.Errorf("alert %d is missing or is not pending", alertID)
	}
	return nil
}

func validateAlertDraft(draft AlertDraft) error {
	if draft.ItemID <= 0 {
		return fmt.Errorf("alert item ID must be positive")
	}
	if draft.Ratio < 0 {
		return fmt.Errorf("alert ratio cannot be negative")
	}
	if draft.Message == "" {
		return fmt.Errorf("alert message cannot be empty")
	}
	if draft.CreatedAt.IsZero() || draft.CreatedAt.UTC().UnixMilli() < 0 {
		return fmt.Errorf("alert creation time must be at or after Unix epoch")
	}
	switch draft.Kind {
	case core.AlertKindAEntry:
		if draft.AEntryAlertID != nil {
			return fmt.Errorf("A-entry alert cannot reference an entry alert")
		}
	case core.AlertKindAImprovement:
		if draft.AEntryAlertID == nil || *draft.AEntryAlertID <= 0 {
			return fmt.Errorf("A-improvement alert requires a positive entry alert ID")
		}
	default:
		return fmt.Errorf("invalid alert kind %q", draft.Kind)
	}
	return nil
}

func validateStoredAlert(alert Alert) error {
	if alert.ID <= 0 || alert.ItemID <= 0 {
		return fmt.Errorf("alert and item IDs must be positive")
	}
	if alert.Message == "" || alert.Ratio < 0 || alert.CreatedAt.UTC().UnixMilli() < 0 {
		return fmt.Errorf("invalid alert content")
	}
	if alert.Status != AlertStatusPending && alert.Status != AlertStatusSent {
		return fmt.Errorf("invalid alert status %q", alert.Status)
	}
	if alert.Status == AlertStatusPending && alert.SentAt != nil {
		return fmt.Errorf("pending alert has sent time")
	}
	if alert.Status == AlertStatusSent && alert.SentAt == nil {
		return fmt.Errorf("sent alert has no sent time")
	}
	return validateAlertDraft(AlertDraft{
		ItemID:        alert.ItemID,
		Kind:          alert.Kind,
		AEntryAlertID: alert.AEntryAlertID,
		Ratio:         alert.Ratio,
		Message:       alert.Message,
		CreatedAt:     alert.CreatedAt,
	})
}
