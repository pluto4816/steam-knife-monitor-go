package store

import "fmt"

func (store *Store) ensureC5PurchaseKindColumn() error {
	rows, err := store.db.Query(`PRAGMA table_info(c5_purchases)`)
	if err != nil {
		return fmt.Errorf("inspect C5 purchase schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect C5 purchase column: %w", err)
		}
		if name == "purchase_kind" {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close C5 purchase schema inspection: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect C5 purchase schema: %w", err)
	}
	if found {
		return nil
	}
	if _, err := store.db.Exec(`
ALTER TABLE c5_purchases
ADD COLUMN purchase_kind TEXT NOT NULL DEFAULT 'AUTO'
CHECK (purchase_kind IN ('AUTO', 'MANUAL_TEST'))`); err != nil {
		return fmt.Errorf("add C5 purchase kind column: %w", err)
	}
	return nil
}
