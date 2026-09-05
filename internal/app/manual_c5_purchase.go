package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/c5"
	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type ManualC5PurchaseTestService struct {
	stateStore *store.Store
	c5Client   c5API
	candidate  catalog.Item
	maxPrice   c5.MoneyFen
	now        func() time.Time
	newTradeNo func(time.Time) (string, error)
}

func NewManualC5PurchaseTestService(
	stateStore *store.Store,
	c5Client c5API,
	candidate catalog.Item,
	maxPrice c5.MoneyFen,
) (*ManualC5PurchaseTestService, error) {
	if stateStore == nil || c5Client == nil {
		return nil, fmt.Errorf("manual C5 purchase test dependencies cannot be nil")
	}
	if candidate.ItemID <= 0 || candidate.MarketHashName == "" || candidate.NameCN == "" {
		return nil, fmt.Errorf("manual C5 purchase test candidate is invalid")
	}
	if maxPrice <= 0 || maxPrice > C5PerOrderLimit {
		return nil, fmt.Errorf("manual C5 purchase test max price must be at most %s", C5PerOrderLimit)
	}
	return &ManualC5PurchaseTestService{
		stateStore: stateStore,
		c5Client:   c5Client,
		candidate:  candidate,
		maxPrice:   maxPrice,
		now:        func() time.Time { return time.Now().UTC() },
		newTradeNo: generateC5TradeNo,
	}, nil
}

func (service *ManualC5PurchaseTestService) Run(ctx context.Context) (PurchaseRunResult, error) {
	var result PurchaseRunResult
	reconciled, placedPurchase, err := reconcileC5Purchases(
		ctx, service.stateStore, service.c5Client, service.now,
	)
	result.ReconciledCount = reconciled
	result.PlacedPurchase = placedPurchase
	if err != nil || placedPurchase != nil {
		return result, err
	}

	now := service.now().UTC()
	budgetDay := shanghaiBudgetDay(now)
	canAttempt, err := service.stateStore.CanAttemptC5Purchase(
		ctx, service.candidate.ItemID, budgetDay,
	)
	if err != nil {
		return result, err
	}
	if !canAttempt {
		return result, store.ErrC5ItemHandled
	}

	balance, err := service.c5Client.Balance(ctx)
	if err != nil {
		return result, fmt.Errorf("query C5 balance: %w", err)
	}
	if balance < service.maxPrice {
		return result, fmt.Errorf(
			"C5 balance %s is below manual test ceiling %s",
			balance,
			service.maxPrice,
		)
	}
	result.ConfirmedCount = 1

	outTradeNo, err := service.newTradeNo(now)
	if err != nil {
		return result, fmt.Errorf("create C5 outTradeNo: %w", err)
	}
	purchase, err := service.stateStore.BeginC5Purchase(ctx, store.BeginC5PurchaseParams{
		ItemID:         service.candidate.ItemID,
		MarketHashName: service.candidate.MarketHashName,
		NameCN:         service.candidate.NameCN,
		OutTradeNo:     outTradeNo,
		QuotedPrice:    service.maxPrice,
		SteamPrice:     "0.00",
		Ratio:          0,
		BudgetDay:      budgetDay,
		Kind:           store.C5PurchaseManualTest,
		CreatedAt:      now,
	})
	if err != nil {
		if errors.Is(err, store.ErrC5ItemHandled) || errors.Is(err, store.ErrC5DailyLimit) {
			return result, err
		}
		return result, fmt.Errorf("reserve manual C5 purchase test: %w", err)
	}

	quickResult, err := service.c5Client.QuickBuy(ctx, c5.QuickBuyRequest{
		OutTradeNo:     outTradeNo,
		MarketHashName: service.candidate.MarketHashName,
		MaxPrice:       service.maxPrice.String(),
	})
	if err != nil {
		if markErr := service.stateStore.MarkC5PurchaseUncertain(
			ctx, purchase.ID, err.Error(), service.now(),
		); markErr != nil {
			return result, fmt.Errorf(
				"manual C5 quick-buy failed and uncertain state could not be persisted: %v: %w",
				err,
				markErr,
			)
		}
		return result, fmt.Errorf("manual C5 quick-buy is uncertain: %w", err)
	}
	if quickResult.ActualPay > purchase.QuotedPrice {
		message := fmt.Sprintf(
			"C5 actual price %s exceeds reserved manual test ceiling %s",
			quickResult.ActualPay,
			purchase.QuotedPrice,
		)
		_ = service.stateStore.MarkC5PurchaseUncertain(ctx, purchase.ID, message, service.now())
		return result, errors.New(message)
	}
	if err := service.stateStore.MarkC5PurchasePlaced(
		ctx,
		purchase.ID,
		quickResult.ActualPay,
		quickResult.OrderID,
		service.now(),
	); err != nil {
		return result, fmt.Errorf("persist placed manual C5 purchase test: %w", err)
	}

	receipt := C5PurchaseReceipt{
		NameCN:        service.candidate.NameCN,
		PurchasePrice: quickResult.ActualPay,
		ManualTest:    true,
	}
	result.PlacedCount = 1
	result.PlacedPurchase = &receipt
	return result, nil
}
