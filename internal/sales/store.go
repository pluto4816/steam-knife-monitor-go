package sales

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsAny(path, ":?\x00") {
		return nil, fmt.Errorf("出售辅助需要持久化 SQLite 文件路径")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: absolute}
	q := u.Query()
	q.Set("_busy_timeout", "5000")
	q.Set("_foreign_keys", "on")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
 CREATE TABLE IF NOT EXISTS sales_lots (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL, hash_name TEXT NOT NULL, platform TEXT NOT NULL,
 bought_on TEXT NOT NULL, quantity INTEGER NOT NULL CHECK(quantity BETWEEN 1 AND 100000),
 unit_cost INTEGER NOT NULL CHECK(unit_cost BETWEEN 1 AND 1000000000),
 sold INTEGER NOT NULL DEFAULT 0 CHECK(sold>=0 AND sold<=quantity),
 quote INTEGER NOT NULL CHECK(quote BETWEEN 0 AND 1000000000), quote_at TEXT NOT NULL,
 target INTEGER NOT NULL CHECK(target BETWEEN 1 AND 100000),
 status TEXT NOT NULL CHECK(status IN ('held','listed')), version INTEGER NOT NULL DEFAULT 1);
 CREATE TABLE IF NOT EXISTS sales_receipts (
 id INTEGER PRIMARY KEY, lot_id INTEGER NOT NULL REFERENCES sales_lots(id),
 quantity INTEGER NOT NULL CHECK(quantity>0), unit_cost INTEGER NOT NULL CHECK(unit_cost>0),
 received INTEGER NOT NULL CHECK(received>0), sold_on TEXT NOT NULL, voided INTEGER NOT NULL DEFAULT 0 CHECK(voided IN (0,1)));
 CREATE TABLE IF NOT EXISTS sales_requests (token TEXT PRIMARY KEY, action TEXT NOT NULL);
 `)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func scanLot(row interface{ Scan(...any) error }) (l Lot, err error) {
	err = row.Scan(&l.ID, &l.Name, &l.HashName, &l.Platform, &l.BoughtOn, &l.Quantity, &l.UnitCost, &l.Sold, &l.Quote, &l.QuoteAt, &l.Target, &l.Status, &l.Version)
	return
}

const lotColumns = `id,name,hash_name,platform,bought_on,quantity,unit_cost,sold,quote,quote_at,target,status,version`

func (s *Store) Lots(ctx context.Context) ([]Lot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+lotColumns+` FROM sales_lots ORDER BY bought_on DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lots []Lot
	for rows.Next() {
		l, e := scanLot(rows)
		if e != nil {
			return nil, e
		}
		lots = append(lots, l)
	}
	return lots, rows.Err()
}
func (s *Store) Receipts(ctx context.Context) ([]Sale, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.lot_id,r.quantity,r.unit_cost,r.received,l.name,r.sold_on,r.voided FROM sales_receipts r JOIN sales_lots l ON l.id=r.lot_id ORDER BY r.sold_on DESC,r.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Sale
	for rows.Next() {
		var r Sale
		if err := rows.Scan(&r.ID, &r.LotID, &r.Quantity, &r.UnitCost, &r.Received, &r.Name, &r.SoldOn, &r.Voided); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
func (s *Store) mutate(ctx context.Context, token, action string, f func(*sql.Tx) error) error {
	if len(token) != 64 {
		return fmt.Errorf("提交标识无效，请刷新页面")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT action FROM sales_requests WHERE token=?`, token).Scan(&previous)
	if err == nil {
		if previous != action {
			return fmt.Errorf("提交标识已用于其他操作，请刷新")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	if err = f(tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sales_requests(token,action) VALUES(?,?)`, token, action); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) SaveLot(ctx context.Context, token string, l Lot) error {
	if err := validateLot(l); err != nil {
		return err
	}
	return s.mutate(ctx, token, fmt.Sprintf("lot:%d", l.ID), func(tx *sql.Tx) error {
		quoteAt := ""
		if l.Quote > 0 {
			quoteAt = time.Now().UTC().Format(time.RFC3339)
		}
		if l.ID == 0 {
			_, err := tx.ExecContext(ctx, `INSERT INTO sales_lots(name,hash_name,platform,bought_on,quantity,unit_cost,quote,quote_at,target,status) VALUES(?,?,?,?,?,?,?,?,?,?)`, l.Name, l.HashName, l.Platform, l.BoughtOn, l.Quantity, l.UnitCost, l.Quote, quoteAt, l.Target, l.Status)
			return err
		}
		old, err := scanLot(tx.QueryRowContext(ctx, `SELECT `+lotColumns+` FROM sales_lots WHERE id=?`, l.ID))
		if err != nil {
			return fmt.Errorf("持仓不存在")
		}
		if old.Version != l.Version {
			return fmt.Errorf("记录已更新，请刷新后重新编辑")
		}
		// Preserve historical identity/cost once any receipt exists, including a voided receipt.
		var count int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sales_receipts WHERE lot_id=?`, l.ID).Scan(&count)
		if err != nil {
			return err
		}
		if count > 0 && (l.Name != old.Name || l.HashName != old.HashName || l.Platform != old.Platform || l.UnitCost != old.UnitCost || l.Quantity != old.Quantity || l.BoughtOn != old.BoughtOn) {
			return fmt.Errorf("已有出售记录，不能改写原买入信息；仍可更新报价、目标和状态")
		}
		if l.Quote == old.Quote && !l.RefreshQuote {
			quoteAt = old.QuoteAt
		}
		_, err = tx.ExecContext(ctx, `UPDATE sales_lots SET name=?,hash_name=?,platform=?,bought_on=?,quantity=?,unit_cost=?,quote=?,quote_at=?,target=?,status=?,version=version+1 WHERE id=?`, l.Name, l.HashName, l.Platform, l.BoughtOn, l.Quantity, l.UnitCost, l.Quote, quoteAt, l.Target, l.Status, l.ID)
		return err
	})
}
func (s *Store) Sell(ctx context.Context, token string, id, version, qty, received int64, day string) error {
	if qty < 1 || qty > MaxQuantity || received < 1 || received > MaxMoney {
		return fmt.Errorf("出售数量和实际总到账必须为正数且在范围内")
	}
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return fmt.Errorf("出售日期不正确")
	}
	return s.mutate(ctx, token, fmt.Sprintf("sell:%d", id), func(tx *sql.Tx) error {
		l, err := scanLot(tx.QueryRowContext(ctx, `SELECT `+lotColumns+` FROM sales_lots WHERE id=?`, id))
		if err != nil {
			return fmt.Errorf("持仓不存在")
		}
		if l.Version != version {
			return fmt.Errorf("记录已更新，请刷新后再登记")
		}
		if qty > l.Remaining() {
			return fmt.Errorf("出售数量超过剩余持仓")
		}
		if day < l.BoughtOn {
			return fmt.Errorf("出售日期不能早于买入日期")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO sales_receipts(lot_id,quantity,unit_cost,received,sold_on) VALUES(?,?,?,?,?)`, id, qty, l.UnitCost, received, day); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE sales_lots SET sold=sold+?,version=version+1 WHERE id=?`, qty, id)
		return err
	})
}
func (s *Store) VoidSale(ctx context.Context, token string, id int64) error {
	return s.mutate(ctx, token, fmt.Sprintf("void:%d", id), func(tx *sql.Tx) error {
		var lot, qty int64
		var voided bool
		if err := tx.QueryRowContext(ctx, `SELECT lot_id,quantity,voided FROM sales_receipts WHERE id=?`, id).Scan(&lot, &qty, &voided); err != nil {
			return fmt.Errorf("出售记录不存在")
		}
		if voided {
			return fmt.Errorf("出售记录已经作废")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sales_receipts SET voided=1 WHERE id=?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE sales_lots SET sold=sold-?,status='held',version=version+1 WHERE id=?`, qty, lot)
		return err
	})
}
