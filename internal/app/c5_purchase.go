package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/c5"
	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type C5PurchaseMode string

const (
	C5PurchaseOff    C5PurchaseMode = "off"
	C5PurchaseDryRun C5PurchaseMode = "dry-run"
	C5PurchaseLive   C5PurchaseMode = "live"

	C5PerOrderLimit c5.MoneyFen = 100
	C5DailyLimit    c5.MoneyFen = 1000
)

type PurchaseRunResult struct {
	ReconciledCount int
	ConfirmedCount  int
	ExcludedCount   int
	DryRunCount     int
	PlacedCount     int
	RejectedCount   int
	PlacedPurchase  *C5PurchaseReceipt
}

type C5PurchaseReceipt struct {
	NameCN        string
	PurchasePrice c5.MoneyFen
	SteamPrice    string
	Ratio         core.RatioBP
}

type PurchaseRunner interface {
	Run(context.Context) (PurchaseRunResult, error)
}

type c5API interface {
	Balance(context.Context) (c5.MoneyFen, error)
	QuickBuy(context.Context, c5.QuickBuyRequest) (c5.QuickBuyResult, error)
	OrderByOutTradeNo(context.Context, string) (c5.Order, error)
}

type C5PurchaseService struct {
	mode         C5PurchaseMode
	smisClient   *collector.SMISClient
	steamPrices  SteamPriceSource
	stateStore   *store.Store
	c5Client     c5API
	candidates   map[int64]catalog.Item
	requestDelay time.Duration
	now          func() time.Time
	newTradeNo   func(time.Time) (string, error)
}

func NewC5PurchaseService(
	mode C5PurchaseMode,
	smisClient *collector.SMISClient,
	steamPrices SteamPriceSource,
	stateStore *store.Store,
	c5Client c5API,
	candidates []catalog.Item,
	requestDelay time.Duration,
) (*C5PurchaseService, error) {
	if mode != C5PurchaseDryRun && mode != C5PurchaseLive {
		return nil, fmt.Errorf("C5 purchase service mode must be dry-run or live")
	}
	if smisClient == nil || steamPrices == nil || stateStore == nil {
		return nil, fmt.Errorf("C5 purchase service dependencies cannot be nil")
	}
	if mode == C5PurchaseLive && c5Client == nil {
		return nil, fmt.Errorf("live C5 purchase service requires a C5 client")
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("C5 purchase service requires candidates")
	}
	if requestDelay < 0 {
		return nil, fmt.Errorf("C5 purchase request delay cannot be negative")
	}
	index := make(map[int64]catalog.Item, len(candidates))
	for _, candidate := range candidates {
		if candidate.ItemID <= 0 || candidate.MarketHashName == "" {
			return nil, fmt.Errorf("C5 purchase candidate is invalid")
		}
		if _, exists := index[candidate.ItemID]; exists {
			return nil, fmt.Errorf("C5 purchase candidate item %d is duplicated", candidate.ItemID)
		}
		index[candidate.ItemID] = candidate
	}
	return &C5PurchaseService{
		mode:         mode,
		smisClient:   smisClient,
		steamPrices:  steamPrices,
		stateStore:   stateStore,
		c5Client:     c5Client,
		candidates:   index,
		requestDelay: requestDelay,
		now:          func() time.Time { return time.Now().UTC() },
		newTradeNo:   generateC5TradeNo,
	}, nil
}

func (service *C5PurchaseService) Run(ctx context.Context) (PurchaseRunResult, error) {
	var result PurchaseRunResult
	if service.mode == C5PurchaseLive {
		reconciled, placedPurchase, err := service.reconcile(ctx)
		result.ReconciledCount = reconciled
		result.PlacedPurchase = placedPurchase
		if err != nil {
			return result, err
		}
		if placedPurchase != nil {
			return result, nil
		}
	}

	states, err := service.stateStore.ListItemStatesByTier(ctx, core.TierA)
	if err != nil {
		return result, fmt.Errorf("list A-tier items for C5 purchase: %w", err)
	}
	requestMade := false
	for _, state := range states {
		candidate, exists := service.candidates[state.ItemID]
		if !exists {
			continue
		}
		if state.MarketHashName != candidate.MarketHashName {
			service.logDryRunExclusion(
				&result,
				state.ItemID,
				candidate.MarketHashName,
				"catalog_identity_mismatch",
				fmt.Sprintf("stored_market_hash_name=%q", state.MarketHashName),
			)
			continue
		}
		now := service.now().UTC()
		budgetDay := shanghaiBudgetDay(now)
		canAttempt, err := service.stateStore.CanAttemptC5Purchase(ctx, state.ItemID, budgetDay)
		if err != nil {
			return result, err
		}
		if !canAttempt {
			service.logDryRunExclusion(
				&result,
				state.ItemID,
				candidate.MarketHashName,
				"already_handled",
				fmt.Sprintf("budget_day=%s", budgetDay),
			)
			continue
		}
		if err := waitBeforeRequest(ctx, service.requestDelay, requestMade); err != nil {
			return result, fmt.Errorf("wait before C5 confirmation item %d: %w", state.ItemID, err)
		}
		item, err := service.smisClient.FetchMarketItem(ctx, state.ItemID)
		requestMade = true
		if err != nil {
			return result, fmt.Errorf("confirm C5 purchase item %d: %w", state.ItemID, err)
		}
		confirmation, err := prefilterC5Purchase(candidate, item)
		if err != nil {
			return result, fmt.Errorf("prefilter C5 purchase item %d: %w", state.ItemID, err)
		}
		if confirmation.ExclusionReason != "" {
			service.logDryRunExclusion(
				&result,
				state.ItemID,
				candidate.MarketHashName,
				confirmation.ExclusionReason,
				confirmation.ExclusionDetails,
			)
			continue
		}

		item, err = applyOfficialSteamPrice(ctx, service.steamPrices, item)
		if err != nil {
			service.logDryRunExclusion(
				&result,
				state.ItemID,
				candidate.MarketHashName,
				"steam_price_unavailable",
				fmt.Sprintf("error=%q", err.Error()),
			)
			return result, fmt.Errorf(
				"confirm Steam price for C5 purchase item %d: %w",
				state.ItemID,
				err,
			)
		}
		tier, err := core.BaseClassify(item.MarketData)
		if err != nil {
			return result, fmt.Errorf("classify Steam-repriced C5 purchase item %d: %w", state.ItemID, err)
		}
		if tier != core.TierA {
			service.logDryRunExclusion(
				&result,
				state.ItemID,
				candidate.MarketHashName,
				"not_base_a_after_steam_reprice",
				fmt.Sprintf(
					"refreshed_tier=%s ratio_bp=%d steam_daily_volume=%d steam_price=%q c5_price=%q",
					tier,
					item.MarketData.Ratio,
					item.MarketData.SteamDailyVolume,
					item.SteamPrice,
					item.LowestPlatformPrice,
				),
			)
			continue
		}
		price := confirmation.Price
		result.ConfirmedCount++
		if service.mode == C5PurchaseDryRun {
			result.DryRunCount++
			log.Printf(
				"C5 purchase dry-run: item_id=%d market_hash_name=%q steam_price=%s ratio=%s max_price=%s",
				item.ItemID,
				item.MarketHashName,
				item.SteamPrice,
				formatRatio(item.MarketData.Ratio),
				item.LowestPlatformPrice,
			)
			continue
		}

		balance, err := service.c5Client.Balance(ctx)
		if err != nil {
			return result, fmt.Errorf("query C5 balance: %w", err)
		}
		if balance < price {
			log.Printf(
				"C5 purchase skipped for insufficient balance: item_id=%d required=%s available=%s",
				item.ItemID,
				price,
				balance,
			)
			continue
		}
		outTradeNo, err := service.newTradeNo(now)
		if err != nil {
			return result, fmt.Errorf("create C5 outTradeNo: %w", err)
		}
		purchase, err := service.stateStore.BeginC5Purchase(ctx, store.BeginC5PurchaseParams{
			ItemID:         item.ItemID,
			MarketHashName: item.MarketHashName,
			NameCN:         candidate.NameCN,
			OutTradeNo:     outTradeNo,
			QuotedPrice:    price,
			SteamPrice:     item.SteamPrice,
			Ratio:          item.MarketData.Ratio,
			BudgetDay:      budgetDay,
			CreatedAt:      now,
		})
		if errors.Is(err, store.ErrC5ItemHandled) {
			continue
		}
		if errors.Is(err, store.ErrC5DailyLimit) {
			break
		}
		if err != nil {
			return result, fmt.Errorf("reserve C5 purchase item %d: %w", item.ItemID, err)
		}

		quickResult, err := service.c5Client.QuickBuy(ctx, c5.QuickBuyRequest{
			OutTradeNo:     outTradeNo,
			MarketHashName: item.MarketHashName,
			MaxPrice:       item.LowestPlatformPrice,
		})
		if err != nil {
			if markErr := service.stateStore.MarkC5PurchaseUncertain(
				ctx, purchase.ID, err.Error(), service.now(),
			); markErr != nil {
				return result, fmt.Errorf(
					"C5 quick-buy failed and uncertain state could not be persisted: %v: %w",
					err,
					markErr,
				)
			}
			return result, fmt.Errorf("C5 quick-buy is uncertain: %w", err)
		}
		if quickResult.ActualPay > purchase.QuotedPrice {
			message := fmt.Sprintf(
				"C5 actual price %s exceeds reserved quote %s",
				quickResult.ActualPay,
				purchase.QuotedPrice,
			)
			_ = service.stateStore.MarkC5PurchaseUncertain(ctx, purchase.ID, message, service.now())
			return result, errors.New(message)
		}
		receipt, err := newC5PurchaseReceipt(
			candidate.NameCN, quickResult.ActualPay, item.SteamPrice,
		)
		if err != nil {
			_ = service.stateStore.MarkC5PurchaseUncertain(ctx, purchase.ID, err.Error(), service.now())
			return result, err
		}
		if err := service.stateStore.MarkC5PurchasePlaced(
			ctx,
			purchase.ID,
			quickResult.ActualPay,
			quickResult.OrderID,
			service.now(),
		); err != nil {
			return result, fmt.Errorf("persist placed C5 purchase: %w", err)
		}
		result.PlacedCount++
		result.PlacedPurchase = &receipt
		return result, nil
	}
	return result, nil
}

type c5PurchaseConfirmation struct {
	Price            c5.MoneyFen
	ExclusionReason  string
	ExclusionDetails string
}

func (service *C5PurchaseService) logDryRunExclusion(
	result *PurchaseRunResult,
	itemID int64,
	marketHashName string,
	reason string,
	details string,
) {
	if service.mode != C5PurchaseDryRun {
		return
	}
	result.ExcludedCount++
	if details != "" {
		details = " " + details
	}
	log.Printf(
		"C5 purchase dry-run excluded: item_id=%d market_hash_name=%q reason=%s%s",
		itemID,
		marketHashName,
		reason,
		details,
	)
}

func (service *C5PurchaseService) reconcile(ctx context.Context) (int, *C5PurchaseReceipt, error) {
	purchases, err := service.stateStore.ListUnresolvedC5Purchases(ctx)
	if err != nil {
		return 0, nil, err
	}
	reconciled := 0
	for _, purchase := range purchases {
		order, err := service.c5Client.OrderByOutTradeNo(ctx, purchase.OutTradeNo)
		if err != nil {
			_ = service.stateStore.MarkC5PurchaseUncertain(
				ctx, purchase.ID, err.Error(), service.now(),
			)
			return reconciled, nil, fmt.Errorf(
				"reconcile C5 purchase %s: %w",
				purchase.OutTradeNo,
				err,
			)
		}
		if order.ActualPay > purchase.QuotedPrice {
			return reconciled, nil, fmt.Errorf(
				"reconciled C5 order %s price %s exceeds reserved quote %s",
				purchase.OutTradeNo,
				order.ActualPay,
				purchase.QuotedPrice,
			)
		}
		receipt, err := newC5PurchaseReceipt(
			purchase.NameCN, order.ActualPay, purchase.SteamPrice,
		)
		if err != nil {
			return reconciled, nil, err
		}
		if err := service.stateStore.MarkC5PurchasePlaced(
			ctx,
			purchase.ID,
			order.ActualPay,
			order.OrderID,
			service.now(),
		); err != nil {
			return reconciled, nil, fmt.Errorf("persist reconciled C5 purchase: %w", err)
		}
		reconciled++
		return reconciled, &receipt, nil
	}
	return reconciled, nil, nil
}

func newC5PurchaseReceipt(
	nameCN string,
	purchasePrice c5.MoneyFen,
	steamPrice string,
) (C5PurchaseReceipt, error) {
	ratio, err := collector.CalculateRatioBP(steamPrice, purchasePrice.String())
	if err != nil {
		return C5PurchaseReceipt{}, fmt.Errorf("calculate actual C5 purchase ratio: %w", err)
	}
	return C5PurchaseReceipt{
		NameCN: nameCN, PurchasePrice: purchasePrice, SteamPrice: steamPrice, Ratio: ratio,
	}, nil
}

func prefilterC5Purchase(candidate catalog.Item, item collector.MarketItem) (c5PurchaseConfirmation, error) {
	if item.ItemID != candidate.ItemID || item.MarketHashName != candidate.MarketHashName {
		return c5PurchaseConfirmation{
			ExclusionReason: "refreshed_identity_mismatch",
			ExclusionDetails: fmt.Sprintf(
				"refreshed_item_id=%d refreshed_market_hash_name=%q",
				item.ItemID,
				item.MarketHashName,
			),
		}, nil
	}
	if item.LowestPlatform != "C5" {
		return c5PurchaseConfirmation{
			ExclusionReason: "lowest_platform_not_c5",
			ExclusionDetails: fmt.Sprintf(
				"lowest_platform=%q lowest_platform_price=%q",
				item.LowestPlatform,
				item.LowestPlatformPrice,
			),
		}, nil
	}
	price, err := c5.ParseMoney(item.LowestPlatformPrice)
	if err != nil {
		return c5PurchaseConfirmation{
			ExclusionReason:  "invalid_c5_price",
			ExclusionDetails: fmt.Sprintf("c5_price=%q", item.LowestPlatformPrice),
		}, nil
	}
	if price <= 0 {
		return c5PurchaseConfirmation{
			ExclusionReason:  "non_positive_c5_price",
			ExclusionDetails: fmt.Sprintf("c5_price=%q", item.LowestPlatformPrice),
		}, nil
	}
	if price > C5PerOrderLimit {
		return c5PurchaseConfirmation{
			ExclusionReason: "above_per_order_limit",
			ExclusionDetails: fmt.Sprintf(
				"c5_price=%s per_order_limit=%s",
				price,
				C5PerOrderLimit,
			),
		}, nil
	}
	return c5PurchaseConfirmation{Price: price}, nil
}

func shanghaiBudgetDay(now time.Time) string {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	return now.In(shanghai).Format("2006-01-02")
}

func generateC5TradeNo(now time.Time) (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "skm-" + shanghaiBudgetDay(now) + "-" + hex.EncodeToString(random), nil
}
