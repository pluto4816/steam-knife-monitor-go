package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

const schema = `
CREATE TABLE IF NOT EXISTS item_state (
    item_id            INTEGER PRIMARY KEY CHECK (item_id > 0),
    market_hash_name   TEXT NOT NULL,
    name_cn            TEXT NOT NULL,
    ratio_bp           INTEGER NOT NULL
                       CHECK (ratio_bp BETWEEN 0 AND 2147483647),
    steam_daily_volume INTEGER NOT NULL
                       CHECK (steam_daily_volume >= 0),
    steam_price        TEXT NOT NULL,
    tier               TEXT NOT NULL
                       CHECK (tier IN ('A', 'B1', 'B2', 'B3', 'C')),
    next_check_at      INTEGER NOT NULL
                       CHECK (next_check_at >= 0)
);

CREATE TABLE IF NOT EXISTS alerts (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id          INTEGER NOT NULL,
    kind             TEXT NOT NULL
                     CHECK (kind IN ('A_ENTRY', 'A_IMPROVEMENT')),
    a_entry_alert_id INTEGER,
    ratio_bp         INTEGER NOT NULL
                     CHECK (ratio_bp BETWEEN 0 AND 2147483647),
    message          TEXT NOT NULL,
    status           TEXT NOT NULL
                     CHECK (status IN ('PENDING', 'SENT')),
    created_at       INTEGER NOT NULL
                     CHECK (created_at >= 0),
    sent_at          INTEGER
                     CHECK (sent_at IS NULL OR sent_at >= 0),
    CHECK (
        (kind = 'A_ENTRY' AND a_entry_alert_id IS NULL)
        OR
        (kind = 'A_IMPROVEMENT' AND a_entry_alert_id IS NOT NULL)
    )
);

CREATE TABLE IF NOT EXISTS targeted_scan_checkpoint (
    singleton           INTEGER PRIMARY KEY CHECK (singleton = 1),
    catalog_fingerprint TEXT NOT NULL CHECK (length(catalog_fingerprint) = 64),
    completed_count     INTEGER NOT NULL CHECK (completed_count > 0),
    last_item_id        INTEGER NOT NULL CHECK (last_item_id > 0)
);

CREATE TABLE IF NOT EXISTS c5_purchases (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id           INTEGER NOT NULL CHECK (item_id > 0),
    market_hash_name  TEXT NOT NULL CHECK (length(market_hash_name) > 0),
    name_cn           TEXT NOT NULL CHECK (length(name_cn) > 0),
    out_trade_no      TEXT NOT NULL UNIQUE CHECK (length(out_trade_no) > 0),
    quoted_price_fen  INTEGER NOT NULL CHECK (quoted_price_fen BETWEEN 1 AND 100),
    steam_price       TEXT NOT NULL CHECK (length(steam_price) > 0),
    ratio_bp          INTEGER NOT NULL CHECK (ratio_bp BETWEEN 0 AND 2147483647),
    budget_day        TEXT NOT NULL CHECK (length(budget_day) = 10),
    status            TEXT NOT NULL CHECK (status IN ('SUBMITTING', 'UNCERTAIN', 'PLACED', 'REJECTED')),
    actual_price_fen  INTEGER
                      CHECK (actual_price_fen IS NULL OR actual_price_fen BETWEEN 1 AND quoted_price_fen),
    c5_order_id       TEXT NOT NULL DEFAULT '',
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at        INTEGER NOT NULL CHECK (updated_at >= 0),
    UNIQUE (item_id, budget_day)
);

CREATE UNIQUE INDEX IF NOT EXISTS c5_one_committed_purchase_per_item
ON c5_purchases(item_id) WHERE status IN ('SUBMITTING', 'UNCERTAIN', 'PLACED');

CREATE INDEX IF NOT EXISTS c5_purchases_status
ON c5_purchases(status, id);`

const itemStateUpsert = `
INSERT INTO item_state (
    item_id, market_hash_name, name_cn, ratio_bp,
    steam_daily_volume, steam_price, tier, next_check_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(item_id) DO UPDATE SET
    market_hash_name = excluded.market_hash_name,
    name_cn = excluded.name_cn,
    ratio_bp = excluded.ratio_bp,
    steam_daily_volume = excluded.steam_daily_volume,
    steam_price = excluded.steam_price,
    tier = excluded.tier,
    next_check_at = excluded.next_check_at`

const maxRatioBP int64 = 1<<31 - 1

type Store struct {
	db *sql.DB
}

type ItemState struct {
	ItemID           int64
	MarketHashName   string
	NameCN           string
	Ratio            core.RatioBP
	SteamDailyVolume int
	SteamPrice       string
	Tier             core.Tier
	NextCheckAt      time.Time
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("SQLite path cannot be empty")
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("access SQLite database: %w", err)
	}
	store := &Store{db: db}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) initSchema() error {
	if _, err := store.db.Exec(schema); err != nil {
		return fmt.Errorf("initialize SQLite schema: %w", err)
	}
	return nil
}

func (store *Store) SaveItemState(ctx context.Context, state ItemState) error {
	if err := validateState(state); err != nil {
		return err
	}
	_, err := store.db.ExecContext(ctx, itemStateUpsert, itemStateArgs(state)...)
	if err != nil {
		return fmt.Errorf("save item state %d: %w", state.ItemID, err)
	}
	return nil
}

func itemStateArgs(state ItemState) []interface{} {
	return []interface{}{
		state.ItemID,
		state.MarketHashName,
		state.NameCN,
		int64(state.Ratio),
		state.SteamDailyVolume,
		state.SteamPrice,
		string(state.Tier),
		state.NextCheckAt.UTC().UnixMilli(),
	}
}

func (store *Store) GetItemState(ctx context.Context, itemID int64) (ItemState, bool, error) {
	if itemID <= 0 {
		return ItemState{}, false, fmt.Errorf("item ID must be positive")
	}
	var state ItemState
	var ratio int64
	var volume int64
	var tier string
	var nextCheckAt int64
	err := store.db.QueryRowContext(ctx, `
SELECT item_id, market_hash_name, name_cn, ratio_bp,
       steam_daily_volume, steam_price, tier, next_check_at
FROM item_state
WHERE item_id = ?`, itemID).Scan(
		&state.ItemID,
		&state.MarketHashName,
		&state.NameCN,
		&ratio,
		&volume,
		&state.SteamPrice,
		&tier,
		&nextCheckAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ItemState{}, false, nil
	}
	if err != nil {
		return ItemState{}, false, fmt.Errorf("get item state %d: %w", itemID, err)
	}
	if ratio < 0 || ratio > maxRatioBP {
		return ItemState{}, false, fmt.Errorf("stored item %d has invalid ratio %d", itemID, ratio)
	}
	if volume < 0 || (strconv.IntSize == 32 && volume > 1<<31-1) {
		return ItemState{}, false, fmt.Errorf("stored item %d has invalid steam daily volume %d", itemID, volume)
	}
	if nextCheckAt < 0 {
		return ItemState{}, false, fmt.Errorf("stored item %d has invalid next check time %d", itemID, nextCheckAt)
	}
	state.Ratio = core.RatioBP(ratio)
	state.SteamDailyVolume = int(volume)
	state.Tier = core.Tier(tier)
	state.NextCheckAt = time.UnixMilli(nextCheckAt).UTC()
	if err := validateState(state); err != nil {
		return ItemState{}, false, fmt.Errorf("stored item %d is invalid: %w", itemID, err)
	}
	return state, true, nil
}

func (store *Store) ListDueItemStates(ctx context.Context, now time.Time) ([]ItemState, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT item_id, market_hash_name, name_cn, ratio_bp,
       steam_daily_volume, steam_price, tier, next_check_at
FROM item_state
WHERE next_check_at <= ?
ORDER BY next_check_at ASC, item_id ASC`, now.UTC().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("list due item states: %w", err)
	}
	defer rows.Close()

	states := []ItemState{}
	for rows.Next() {
		var state ItemState
		var ratio, volume, nextCheckAt int64
		var tier string
		if err := rows.Scan(
			&state.ItemID,
			&state.MarketHashName,
			&state.NameCN,
			&ratio,
			&volume,
			&state.SteamPrice,
			&tier,
			&nextCheckAt,
		); err != nil {
			return nil, fmt.Errorf("scan due item state: %w", err)
		}
		if ratio < 0 || ratio > maxRatioBP {
			return nil, fmt.Errorf("stored item %d has invalid ratio %d", state.ItemID, ratio)
		}
		if volume < 0 || (strconv.IntSize == 32 && volume > 1<<31-1) {
			return nil, fmt.Errorf("stored item %d has invalid steam daily volume %d", state.ItemID, volume)
		}
		if nextCheckAt < 0 {
			return nil, fmt.Errorf("stored item %d has invalid next check time %d", state.ItemID, nextCheckAt)
		}
		state.Ratio = core.RatioBP(ratio)
		state.SteamDailyVolume = int(volume)
		state.Tier = core.Tier(tier)
		state.NextCheckAt = time.UnixMilli(nextCheckAt).UTC()
		if err := validateState(state); err != nil {
			return nil, fmt.Errorf("stored item %d is invalid: %w", state.ItemID, err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list due item states: %w", err)
	}
	return states, nil
}

func (store *Store) Close() error {
	return store.db.Close()
}

func validateState(state ItemState) error {
	if state.ItemID <= 0 {
		return fmt.Errorf("item ID must be positive")
	}
	if state.Ratio < 0 {
		return fmt.Errorf("ratio cannot be negative")
	}
	if state.SteamDailyVolume < 0 {
		return fmt.Errorf("steam daily volume cannot be negative")
	}
	if !validTier(state.Tier) {
		return fmt.Errorf("invalid tier %q", state.Tier)
	}
	if state.NextCheckAt.IsZero() {
		return fmt.Errorf("next check time cannot be zero")
	}
	if state.NextCheckAt.UTC().UnixMilli() < 0 {
		return fmt.Errorf("next check time cannot be before Unix epoch")
	}
	return nil
}

func validTier(tier core.Tier) bool {
	switch tier {
	case core.TierA, core.TierB1, core.TierB2, core.TierB3, core.TierC:
		return true
	default:
		return false
	}
}
