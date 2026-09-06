package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/eco"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

// The interface permits tests of external payment effects without live purchases.
type ecoTestAPI interface {
	Balance(context.Context) (eco.MoneyFen, error)
	BuyTestItem(context.Context, string) (eco.BuyResult, error)
	Order(context.Context, string) (eco.Order, error)
}

type ECOTestResult struct {
	Submitted  bool
	Reconciled bool
	Order      eco.Order
}

// RunManualECOTest is one bounded attempt for P250 Re.built FT only. There are no
// SMIS, Steam, C5, QQ or ordinary monitoring dependencies in this execution path.
func RunManualECOTest(ctx context.Context, s *store.Store, client ecoTestAPI) (ECOTestResult, error) {
	var result ECOTestResult
	if s == nil || client == nil {
		return result, errors.New("ECO test dependencies missing")
	}
	if err := s.InitECOTest(ctx); err != nil {
		return result, err
	}
	rows, err := s.ListECOPurchases(ctx)
	if err != nil {
		return result, err
	}
	var target []store.ECOPurchase
	for _, row := range rows {
		if row.HashName == eco.TestHashName {
			target = append(target, row)
		}
	}
	rows = target
	if len(rows) > 0 {
		if len(rows) != 1 {
			return result, errors.New("unexpected ECO ledger identities; purchase blocked")
		}
		// A repeated invocation can only inspect the original order. Not-found is
		// not proof of no order, so reservations are never cleared automatically.
		order, err := client.Order(ctx, rows[0].MerchantNo)
		if err != nil {
			return result, fmt.Errorf("ECO original order unresolved; no resubmission: %w", err)
		}
		if err := s.SaveECOOrder(ctx, rows[0].ID, order); err != nil {
			return result, err
		}
		result.Reconciled = true
		result.Order = order
		return result, nil
	}
	balance, err := client.Balance(ctx)
	if err != nil {
		return result, fmt.Errorf("ECO balance check: %w", err)
	}
	if balance < eco.PerOrderLimit {
		return result, errors.New("ECO available balance below CNY 1.00; purchase not submitted")
	}
	if err := waitECOStep(ctx); err != nil {
		return result, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return result, errors.New("generate ECO merchant number failed")
	}
	merchantNo := "eco-test-" + hex.EncodeToString(random[:])
	purchase, err := s.ReserveECOTest(ctx, merchantNo, time.Now())
	if err != nil {
		return result, err
	}
	result.Submitted = true
	bought, err := client.BuyTestItem(ctx, merchantNo)
	if err != nil {
		// Use a separate bounded context so cancellation after submission does not
		// prevent recording uncertainty. SUBMITTING itself also blocks resubmission.
		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if markErr := s.MarkECOUncertain(persistCtx, purchase.ID); markErr != nil {
			return result, fmt.Errorf("ECO submission uncertain and ledger update failed: %v; %w", markErr, err)
		}
		return result, fmt.Errorf("ECO submission uncertain; only original-order queries are permitted: %w", err)
	}
	if err := s.MarkECOPlaced(ctx, purchase.ID, bought); err != nil {
		return result, fmt.Errorf("ECO order created but persistence failed; do not resubmit: %w", err)
	}
	result.Order = eco.Order{OrderNo: bought.OrderNo, MerchantNo: merchantNo, HashName: eco.TestHashName, Paid: bought.Paid}
	if err := waitECOStep(ctx); err != nil {
		return result, err
	}
	order, err := client.Order(ctx, merchantNo)
	if err != nil {
		return result, fmt.Errorf("ECO order created; detail check failed, no resubmission: %w", err)
	}
	if err := s.SaveECOOrder(ctx, purchase.ID, order); err != nil {
		return result, err
	}
	result.Order = order
	return result, nil
}

func waitECOStep(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
