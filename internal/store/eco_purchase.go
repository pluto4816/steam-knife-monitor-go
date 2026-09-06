package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/eco"
)

// Initialize only when ECO live purchasing (or the manual test) is explicitly enabled.
const ecoSchema = `CREATE TABLE IF NOT EXISTS eco_purchases (
 id INTEGER PRIMARY KEY,
 market_hash_name TEXT NOT NULL UNIQUE CHECK(length(market_hash_name)>0),
 merchant_no TEXT NOT NULL UNIQUE CHECK(length(merchant_no) BETWEEN 1 AND 64),
 budget_day TEXT NOT NULL CHECK(length(budget_day)=10),
 reserved_fen INTEGER NOT NULL CHECK(reserved_fen BETWEEN 1 AND 100),
 status TEXT NOT NULL CHECK(status IN ('SUBMITTING','UNCERTAIN','PLACED')),
 order_no TEXT NOT NULL DEFAULT '',
 paid_fen INTEGER CHECK(paid_fen BETWEEN 1 AND reserved_fen),
 order_state INTEGER,
 purchase_kind TEXT NOT NULL DEFAULT 'MANUAL_TEST' CHECK(purchase_kind IN ('MANUAL_TEST','AUTO')),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 item_id INTEGER NOT NULL DEFAULT 0,
 name_cn TEXT NOT NULL DEFAULT '',
 steam_price TEXT NOT NULL DEFAULT ''
 )`

func (s *Store) InitECOTest(ctx context.Context) error { return s.InitECOPurchases(ctx) }

func (s *Store) InitECOPurchases(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var original string
	err = tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='eco_purchases'`).Scan(&original)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if original != "" && !strings.Contains(original, "'AUTO'") {
		if _, err = tx.ExecContext(ctx, `ALTER TABLE eco_purchases RENAME TO eco_purchases_manual_v1`); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, ecoSchema); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO eco_purchases (id,market_hash_name,merchant_no,budget_day,reserved_fen,status,order_no,paid_fen,order_state,purchase_kind,created_at,updated_at)
 SELECT id,market_hash_name,merchant_no,budget_day,reserved_fen,status,order_no,paid_fen,order_state,purchase_kind,created_at,updated_at FROM eco_purchases_manual_v1`); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DROP TABLE eco_purchases_manual_v1`); err != nil {
			return err
		}
	} else if _, err = tx.ExecContext(ctx, ecoSchema); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS eco_purchase_item ON eco_purchases(item_id) WHERE item_id>0`); err != nil {
		return err
	}
	return tx.Commit()
}

type ECOPurchase struct {
	ID         int64
	ItemID     int64
	NameCN     string
	SteamPrice string
	Reserved   eco.MoneyFen
	Kind       string
	HashName   string
	MerchantNo string
	Status     string
	OrderNo    string
	Paid       eco.MoneyFen
	OrderState *int
}

func (s *Store) ListECOPurchases(ctx context.Context) ([]ECOPurchase, error) {
	exists, err := s.ecoTableExists(ctx)
	if err != nil || !exists {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT item_id,name_cn,steam_price,reserved_fen,purchase_kind,id, market_hash_name, merchant_no, status, order_no, COALESCE(paid_fen,0), order_state FROM eco_purchases ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ECOPurchase
	for rows.Next() {
		var p ECOPurchase
		var state sql.NullInt64
		if err := rows.Scan(&p.ItemID, &p.NameCN, &p.SteamPrice, &p.Reserved, &p.Kind, &p.ID, &p.HashName, &p.MerchantNo, &p.Status, &p.OrderNo, &p.Paid, &state); err != nil {
			return nil, err
		}
		if state.Valid {
			v := int(state.Int64)
			p.OrderState = &v
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

var ErrECOPurchaseBlocked = errors.New("ECO item already handled, order unresolved or daily budget exhausted; no new submission")

func (s *Store) ReserveECOTest(ctx context.Context, merchantNo string, at time.Time) (ECOPurchase, error) {
	return s.ReserveECOPurchase(ctx, ECOReservation{HashName: eco.TestHashName, NameCN: eco.TestNameCN, MerchantNo: merchantNo, Ceiling: eco.PerOrderLimit, Kind: "MANUAL_TEST", At: at})
}

type ECOReservation struct {
	ItemID                                         int64
	HashName, NameCN, MerchantNo, SteamPrice, Kind string
	Ceiling                                        eco.MoneyFen
	At                                             time.Time
}

func (s *Store) ReserveECOPurchase(ctx context.Context, p ECOReservation) (ECOPurchase, error) {
	if p.HashName == "" || p.NameCN == "" || p.MerchantNo == "" || len(p.MerchantNo) > 64 || p.Ceiling <= 0 || p.Ceiling > 100 || p.At.IsZero() || (p.Kind != "MANUAL_TEST" && p.Kind != "AUTO") {
		return ECOPurchase{}, errors.New("invalid ECO reservation")
	}
	if p.Kind == "AUTO" && (p.ItemID <= 0 || p.SteamPrice == "") {
		return ECOPurchase{}, errors.New("invalid ECO automatic evidence")
	}
	day := p.At.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ECOPurchase{}, err
	}
	defer tx.Rollback()
	var count int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM eco_purchases WHERE market_hash_name=? OR (item_id>0 AND item_id=?) OR status IN ('SUBMITTING','UNCERTAIN') OR (status='PLACED' AND order_state IS NULL)`, p.HashName, p.ItemID).Scan(&count)
	if err != nil {
		return ECOPurchase{}, err
	}
	if count > 0 {
		return ECOPurchase{}, ErrECOPurchaseBlocked
	}
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM c5_purchases WHERE status IN ('SUBMITTING','UNCERTAIN') OR (status='PLACED' AND (market_hash_name=? OR item_id=?))`, p.HashName, p.ItemID).Scan(&count)
	if err != nil {
		return ECOPurchase{}, err
	}
	if count > 0 {
		return ECOPurchase{}, ErrECOPurchaseBlocked
	}
	var used int64
	err = tx.QueryRowContext(ctx, `SELECT
 (SELECT COALESCE(SUM(CASE WHEN status='PLACED' THEN actual_price_fen WHEN status IN ('SUBMITTING','UNCERTAIN') THEN quoted_price_fen ELSE 0 END),0) FROM c5_purchases WHERE budget_day=?) +
 (SELECT COALESCE(SUM(reserved_fen),0) FROM eco_purchases WHERE budget_day=?)`, day, day).Scan(&used)
	if err != nil {
		return ECOPurchase{}, err
	}
	if used > 1000-int64(p.Ceiling) {
		return ECOPurchase{}, ErrECOPurchaseBlocked
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO eco_purchases(market_hash_name,merchant_no,budget_day,reserved_fen,status,created_at,updated_at,purchase_kind,item_id,name_cn,steam_price) VALUES(?,?,?,?,'SUBMITTING',?,?,?,?,?,?)`, p.HashName, p.MerchantNo, day, p.Ceiling, p.At.UnixMilli(), p.At.UnixMilli(), p.Kind, p.ItemID, p.NameCN, p.SteamPrice)
	if err != nil {
		return ECOPurchase{}, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return ECOPurchase{}, err
	}
	if err := tx.Commit(); err != nil {
		return ECOPurchase{}, err
	}
	return ECOPurchase{ID: id, ItemID: p.ItemID, NameCN: p.NameCN, HashName: p.HashName, MerchantNo: p.MerchantNo, Status: "SUBMITTING", Reserved: p.Ceiling, SteamPrice: p.SteamPrice, Kind: p.Kind}, nil
}

// Read-only, including on old databases with no ECO table (dry-run performs no migration).
func (s *Store) ecoTableExists(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='eco_purchases'`).Scan(&n)
	return n > 0, err
}

func (s *Store) CanAttemptECO(ctx context.Context, itemID int64, hash string, ceiling eco.MoneyFen, at time.Time) (bool, error) {
	if hash == "" || ceiling <= 0 || ceiling > 100 {
		return false, errors.New("invalid ECO eligibility")
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM c5_purchases WHERE status IN ('SUBMITTING','UNCERTAIN') OR (status='PLACED' AND (market_hash_name=? OR item_id=?))`, hash, itemID).Scan(&count)
	if err != nil || count > 0 {
		return false, err
	}
	day := at.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
	var used int64
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN status='PLACED' THEN actual_price_fen WHEN status IN ('SUBMITTING','UNCERTAIN') THEN quoted_price_fen ELSE 0 END),0) FROM c5_purchases WHERE budget_day=?`, day).Scan(&used)
	if err != nil {
		return false, err
	}
	exists, err := s.ecoTableExists(ctx)
	if err != nil {
		return false, err
	}
	if exists {
		// Works for the old manual-only schema as well.
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM eco_purchases WHERE market_hash_name=? OR status IN ('SUBMITTING','UNCERTAIN') OR (status='PLACED' AND order_state IS NULL)`, hash).Scan(&count)
		if err != nil || count > 0 {
			return false, err
		}
		var schemaText string
		if err = s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name='eco_purchases'`).Scan(&schemaText); err != nil {
			return false, err
		}
		if strings.Contains(schemaText, "'AUTO'") && itemID > 0 {
			if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM eco_purchases WHERE item_id=?`, itemID).Scan(&count); err != nil {
				return false, err
			}
			if count > 0 {
				return false, nil
			}
		}
		var reserved int64
		if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(reserved_fen),0) FROM eco_purchases WHERE budget_day=?`, day).Scan(&reserved); err != nil {
			return false, err
		}
		used += reserved
	}
	return used <= 1000-int64(ceiling), nil
}

func (s *Store) MarkECOUncertain(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE eco_purchases SET status='UNCERTAIN', updated_at=? WHERE id=? AND status IN ('SUBMITTING','UNCERTAIN')`, time.Now().UnixMilli(), id)
	return requireECOUpdate(res, err)
}

func (s *Store) MarkECOPlaced(ctx context.Context, id int64, result eco.BuyResult) error {
	if result.OrderNo == "" || result.MerchantNo == "" || result.Paid <= 0 || result.Paid > eco.PerOrderLimit {
		return errors.New("invalid ECO placed result")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE eco_purchases SET status='PLACED',order_no=?,paid_fen=?,updated_at=? WHERE id=? AND merchant_no=? AND paid_fen IS NULL AND reserved_fen>=? AND status IN ('SUBMITTING','UNCERTAIN')`, result.OrderNo, result.Paid, time.Now().UnixMilli(), id, result.MerchantNo, result.Paid)
	return requireECOUpdate(res, err)
}

func (s *Store) SaveECOOrder(ctx context.Context, id int64, order eco.Order) error {
	if order.OrderNo == "" || order.MerchantNo == "" || order.HashName == "" || order.Paid <= 0 || order.Paid > eco.PerOrderLimit {
		return errors.New("invalid ECO order")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE eco_purchases SET status='PLACED',order_no=?,paid_fen=?,order_state=?,updated_at=? WHERE id=? AND merchant_no=? AND market_hash_name=? AND reserved_fen>=? AND (order_no='' OR order_no=?)`, order.OrderNo, order.Paid, order.State, time.Now().UnixMilli(), id, order.MerchantNo, order.HashName, order.Paid, order.OrderNo)
	return requireECOUpdate(res, err)
}

func requireECOUpdate(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("ECO ledger update affected %d rows; expected 1", n)
	}
	return nil
}
