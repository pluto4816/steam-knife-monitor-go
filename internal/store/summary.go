package store

import (
	"context"
	"fmt"
)

type ItemTierCounts struct {
	A  int
	B1 int
	B2 int
	B3 int
	C  int
}

func (store *Store) CountItemStatesByTier(ctx context.Context) (ItemTierCounts, error) {
	var counts ItemTierCounts
	err := store.db.QueryRowContext(ctx, `
SELECT
    COUNT(*) FILTER (WHERE tier = 'A'),
    COUNT(*) FILTER (WHERE tier = 'B1'),
    COUNT(*) FILTER (WHERE tier = 'B2'),
    COUNT(*) FILTER (WHERE tier = 'B3'),
    COUNT(*) FILTER (WHERE tier = 'C')
FROM item_state`).Scan(
		&counts.A,
		&counts.B1,
		&counts.B2,
		&counts.B3,
		&counts.C,
	)
	if err != nil {
		return ItemTierCounts{}, fmt.Errorf("count item states by tier: %w", err)
	}
	return counts, nil
}
