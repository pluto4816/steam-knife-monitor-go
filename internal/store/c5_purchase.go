package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/c5"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

const (
	c5PerOrderLimitFen c5.MoneyFen = 100
	c5DailyLimitFen    c5.MoneyFen = 1000
)

var (
	ErrC5ItemHandled = errors.New("C5 item already handled")
	ErrC5DailyLimit  = errors.New("C5 daily limit exceeded")
)

type C5PurchaseStatus string
type C5PurchaseKind string

const (
	C5PurchaseAuto       C5PurchaseKind = "AUTO"
	C5PurchaseManualTest C5PurchaseKind = "MANUAL_TEST"
)

const (
	C5PurchaseSubmitting C5PurchaseStatus = "SUBMITTING"
	C5PurchaseUncertain  C5PurchaseStatus = "UNCERTAIN"
	C5PurchasePlaced     C5PurchaseStatus = "PLACED"
	C5PurchaseRejected   C5PurchaseStatus = "REJECTED"
)

type C5Purchase struct {
	ID             int64
	ItemID         int64
	MarketHashName string
	NameCN         string
	OutTradeNo     string
	QuotedPrice    c5.MoneyFen
	SteamPrice     string
	Ratio          core.RatioBP
	BudgetDay      string
	Status         C5PurchaseStatus
	ActualPrice    c5.MoneyFen
	C5OrderID      string
	LastError      string
	Kind           C5PurchaseKind
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type BeginC5PurchaseParams struct {
	ItemID         int64
	MarketHashName string
	NameCN         string
	OutTradeNo     string
	QuotedPrice    c5.MoneyFen
	SteamPrice     string
	Ratio          core.RatioBP
	BudgetDay      string
	Kind           C5PurchaseKind
	CreatedAt      time.Time
}

func (store *Store) CanAttemptC5Purchase(ctx context.Context, itemID int64, budgetDay string) (bool, error) {
	if itemID <= 0 {
		return false, fmt.Errorf("C5 purchase item ID must be positive")
	}
	if err := validateBudgetDay(budgetDay); err != nil {
		return false, err
	}
	var count int
	err := store.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM c5_purchases
WHERE item_id = ?
  AND (budget_day = ? OR status IN ('SUBMITTING', 'UNCERTAIN', 'PLACED'))`,
		itemID,
		budgetDay,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check C5 purchase eligibility for item %d: %w", itemID, err)
	}
	return count == 0, nil
}

func (store *Store) BeginC5Purchase(ctx context.Context, params BeginC5PurchaseParams) (C5Purchase, error) {
	if params.Kind == "" {
		params.Kind = C5PurchaseAuto
	}
	if err := validateBeginC5Purchase(params); err != nil {
		return C5Purchase{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return C5Purchase{}, fmt.Errorf("begin C5 purchase transaction: %w", err)
	}
	defer transaction.Rollback()

	var handled int
	err = transaction.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM c5_purchases
WHERE item_id = ?
  AND (budget_day = ? OR status IN ('SUBMITTING', 'UNCERTAIN', 'PLACED'))`,
		params.ItemID,
		params.BudgetDay,
	).Scan(&handled)
	if err != nil {
		return C5Purchase{}, fmt.Errorf("check C5 item reservation: %w", err)
	}
	if handled != 0 {
		return C5Purchase{}, ErrC5ItemHandled
	}

	var reserved int64
	err = transaction.QueryRowContext(ctx, `
SELECT COALESCE(SUM(
    CASE
        WHEN status = 'PLACED' THEN actual_price_fen
        WHEN status IN ('SUBMITTING', 'UNCERTAIN') THEN quoted_price_fen
        ELSE 0
    END
), 0)
FROM c5_purchases
WHERE budget_day = ?`, params.BudgetDay).Scan(&reserved)
	if err != nil {
		return C5Purchase{}, fmt.Errorf("sum C5 daily reservations: %w", err)
	}
	if reserved > int64(c5DailyLimitFen)-int64(params.QuotedPrice) {
		return C5Purchase{}, ErrC5DailyLimit
	}

	createdAt := params.CreatedAt.UTC().UnixMilli()
	result, err := transaction.ExecContext(ctx, `
INSERT INTO c5_purchases (
    item_id, market_hash_name, name_cn, out_trade_no, quoted_price_fen,
    steam_price, ratio_bp, budget_day, status, created_at, updated_at, purchase_kind
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'SUBMITTING', ?, ?, ?)`,
		params.ItemID,
		params.MarketHashName,
		params.NameCN,
		params.OutTradeNo,
		int64(params.QuotedPrice),
		params.SteamPrice,
		int64(params.Ratio),
		params.BudgetDay,
		createdAt,
		createdAt,
		string(params.Kind),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return C5Purchase{}, ErrC5ItemHandled
		}
		return C5Purchase{}, fmt.Errorf("reserve C5 purchase: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return C5Purchase{}, fmt.Errorf("read C5 purchase ID: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return C5Purchase{}, fmt.Errorf("commit C5 purchase reservation: %w", err)
	}
	return C5Purchase{
		ID:             id,
		ItemID:         params.ItemID,
		MarketHashName: params.MarketHashName,
		NameCN:         params.NameCN,
		OutTradeNo:     params.OutTradeNo,
		QuotedPrice:    params.QuotedPrice,
		SteamPrice:     params.SteamPrice,
		Ratio:          params.Ratio,
		BudgetDay:      params.BudgetDay,
		Status:         C5PurchaseSubmitting,
		Kind:           params.Kind,
		CreatedAt:      params.CreatedAt.UTC(),
		UpdatedAt:      params.CreatedAt.UTC(),
	}, nil
}

func (store *Store) ListUnresolvedC5Purchases(ctx context.Context) ([]C5Purchase, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT id, item_id, market_hash_name, name_cn, out_trade_no, quoted_price_fen,
       steam_price, ratio_bp, budget_day, status, actual_price_fen,
       c5_order_id, last_error, created_at, updated_at, purchase_kind
FROM c5_purchases
WHERE status IN ('SUBMITTING', 'UNCERTAIN')
ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list unresolved C5 purchases: %w", err)
	}
	defer rows.Close()

	var purchases []C5Purchase
	for rows.Next() {
		purchase, err := scanC5Purchase(rows)
		if err != nil {
			return nil, err
		}
		purchases = append(purchases, purchase)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list unresolved C5 purchases: %w", err)
	}
	return purchases, nil
}

func (store *Store) MarkC5PurchaseUncertain(ctx context.Context, id int64, message string, at time.Time) error {
	return store.updateC5Status(
		ctx, id, C5PurchaseUncertain, message, "", 0, at,
		[]C5PurchaseStatus{C5PurchaseSubmitting, C5PurchaseUncertain},
	)
}

func (store *Store) MarkC5PurchaseRejected(ctx context.Context, id int64, message string, at time.Time) error {
	return store.updateC5Status(
		ctx, id, C5PurchaseRejected, message, "", 0, at,
		[]C5PurchaseStatus{C5PurchaseSubmitting},
	)
}

func (store *Store) MarkC5PurchasePlaced(
	ctx context.Context,
	id int64,
	actualPrice c5.MoneyFen,
	orderID string,
	at time.Time,
) error {
	if actualPrice <= 0 {
		return fmt.Errorf("C5 actual price must be positive")
	}
	if strings.TrimSpace(orderID) == "" {
		return fmt.Errorf("C5 order ID cannot be empty")
	}
	return store.updateC5Status(
		ctx, id, C5PurchasePlaced, "", orderID, actualPrice, at,
		[]C5PurchaseStatus{C5PurchaseSubmitting, C5PurchaseUncertain},
	)
}

func (store *Store) updateC5Status(
	ctx context.Context,
	id int64,
	status C5PurchaseStatus,
	message, orderID string,
	actualPrice c5.MoneyFen,
	at time.Time,
	from []C5PurchaseStatus,
) error {
	if id <= 0 {
		return fmt.Errorf("C5 purchase ID must be positive")
	}
	if at.IsZero() || at.UTC().UnixMilli() < 0 {
		return fmt.Errorf("C5 purchase update time is invalid")
	}
	placeholders := make([]string, len(from))
	arguments := []interface{}{string(status), message, orderID, nil, at.UTC().UnixMilli(), id}
	if actualPrice > 0 {
		arguments[3] = int64(actualPrice)
	}
	for index, source := range from {
		placeholders[index] = "?"
		arguments = append(arguments, string(source))
	}
	query := `
UPDATE c5_purchases
SET status = ?, last_error = ?, c5_order_id = ?, actual_price_fen = ?, updated_at = ?
WHERE id = ? AND status IN (` + strings.Join(placeholders, ",") + ")"
	if status == C5PurchasePlaced {
		query += " AND ? <= quoted_price_fen"
		arguments = append(arguments, int64(actualPrice))
	}
	result, err := store.db.ExecContext(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("mark C5 purchase %d %s: %w", id, status, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read C5 purchase %d transition result: %w", id, err)
	}
	if affected != 1 {
		return fmt.Errorf("C5 purchase %d cannot transition to %s", id, status)
	}
	return nil
}

func (store *Store) ListItemStatesByTier(ctx context.Context, tier core.Tier) ([]ItemState, error) {
	if !validTier(tier) {
		return nil, fmt.Errorf("invalid tier %q", tier)
	}
	rows, err := store.db.QueryContext(ctx, `
SELECT item_id, market_hash_name, name_cn, ratio_bp,
       steam_daily_volume, steam_price, tier, next_check_at
FROM item_state
WHERE tier = ?
ORDER BY item_id ASC`, string(tier))
	if err != nil {
		return nil, fmt.Errorf("list %s item states: %w", tier, err)
	}
	defer rows.Close()

	var states []ItemState
	for rows.Next() {
		var state ItemState
		var ratio, volume, nextCheckAt int64
		var storedTier string
		if err := rows.Scan(
			&state.ItemID, &state.MarketHashName, &state.NameCN, &ratio, &volume,
			&state.SteamPrice, &storedTier, &nextCheckAt,
		); err != nil {
			return nil, fmt.Errorf("scan %s item state: %w", tier, err)
		}
		if ratio < 0 || ratio > maxRatioBP ||
			volume < 0 || (strconv.IntSize == 32 && volume > 1<<31-1) ||
			nextCheckAt < 0 {
			return nil, fmt.Errorf("stored item %d has invalid values", state.ItemID)
		}
		state.Ratio = core.RatioBP(ratio)
		state.SteamDailyVolume = int(volume)
		state.Tier = core.Tier(storedTier)
		state.NextCheckAt = time.UnixMilli(nextCheckAt).UTC()
		if err := validateState(state); err != nil {
			return nil, fmt.Errorf("stored item %d is invalid: %w", state.ItemID, err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list %s item states: %w", tier, err)
	}
	return states, nil
}

func scanC5Purchase(row *sql.Rows) (C5Purchase, error) {
	var purchase C5Purchase
	var quotedPrice, ratio, createdAt, updatedAt int64
	var actualPrice sql.NullInt64
	var status string
	err := row.Scan(
		&purchase.ID, &purchase.ItemID, &purchase.MarketHashName, &purchase.NameCN,
		&purchase.OutTradeNo, &quotedPrice, &purchase.SteamPrice, &ratio,
		&purchase.BudgetDay, &status, &actualPrice, &purchase.C5OrderID,
		&purchase.LastError, &createdAt, &updatedAt, &purchase.Kind,
	)
	if err != nil {
		return C5Purchase{}, fmt.Errorf("scan C5 purchase: %w", err)
	}
	purchase.QuotedPrice = c5.MoneyFen(quotedPrice)
	purchase.Ratio = core.RatioBP(ratio)
	purchase.Status = C5PurchaseStatus(status)
	if actualPrice.Valid {
		purchase.ActualPrice = c5.MoneyFen(actualPrice.Int64)
	}
	purchase.CreatedAt = time.UnixMilli(createdAt).UTC()
	purchase.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return purchase, nil
}

func validateBeginC5Purchase(params BeginC5PurchaseParams) error {
	if params.ItemID <= 0 {
		return fmt.Errorf("C5 purchase item ID must be positive")
	}
	if strings.TrimSpace(params.MarketHashName) == "" ||
		strings.TrimSpace(params.NameCN) == "" ||
		strings.TrimSpace(params.OutTradeNo) == "" ||
		strings.TrimSpace(params.SteamPrice) == "" {
		return fmt.Errorf("C5 purchase identity and price fields cannot be empty")
	}
	if params.QuotedPrice <= 0 || params.QuotedPrice > c5PerOrderLimitFen {
		return fmt.Errorf("C5 purchase price limits are invalid")
	}
	if params.Ratio < 0 {
		return fmt.Errorf("C5 purchase ratio cannot be negative")
	}
	if err := validateBudgetDay(params.BudgetDay); err != nil {
		return err
	}
	if params.Kind != C5PurchaseAuto && params.Kind != C5PurchaseManualTest {
		return fmt.Errorf("C5 purchase kind is invalid")
	}
	if params.CreatedAt.IsZero() || params.CreatedAt.UTC().UnixMilli() < 0 {
		return fmt.Errorf("C5 purchase creation time is invalid")
	}
	return nil
}

func validateBudgetDay(value string) error {
	if len(value) != len("2006-01-02") {
		return fmt.Errorf("C5 budget day must use YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fmt.Errorf("C5 budget day must use YYYY-MM-DD")
	}
	return nil
}
