package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

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
                       CHECK (tier IN ('A', 'B1', 'B2', 'B3', 'C'))
);`

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
	_, err := store.db.ExecContext(ctx, `
INSERT INTO item_state (
    item_id, market_hash_name, name_cn, ratio_bp,
    steam_daily_volume, steam_price, tier
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(item_id) DO UPDATE SET
    market_hash_name = excluded.market_hash_name,
    name_cn = excluded.name_cn,
    ratio_bp = excluded.ratio_bp,
    steam_daily_volume = excluded.steam_daily_volume,
    steam_price = excluded.steam_price,
    tier = excluded.tier`,
		state.ItemID,
		state.MarketHashName,
		state.NameCN,
		int64(state.Ratio),
		state.SteamDailyVolume,
		state.SteamPrice,
		string(state.Tier),
	)
	if err != nil {
		return fmt.Errorf("save item state %d: %w", state.ItemID, err)
	}
	return nil
}

func (store *Store) GetItemState(ctx context.Context, itemID int64) (ItemState, bool, error) {
	if itemID <= 0 {
		return ItemState{}, false, fmt.Errorf("item ID must be positive")
	}
	var state ItemState
	var ratio int64
	var volume int64
	var tier string
	err := store.db.QueryRowContext(ctx, `
SELECT item_id, market_hash_name, name_cn, ratio_bp,
       steam_daily_volume, steam_price, tier
FROM item_state
WHERE item_id = ?`, itemID).Scan(
		&state.ItemID,
		&state.MarketHashName,
		&state.NameCN,
		&ratio,
		&volume,
		&state.SteamPrice,
		&tier,
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
	state.Ratio = core.RatioBP(ratio)
	state.SteamDailyVolume = int(volume)
	state.Tier = core.Tier(tier)
	if err := validateState(state); err != nil {
		return ItemState{}, false, fmt.Errorf("stored item %d is invalid: %w", itemID, err)
	}
	return state, true, nil
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
