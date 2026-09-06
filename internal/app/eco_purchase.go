package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/eco"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type ECOPurchaseMode string

const (
	ECOPurchaseOff    ECOPurchaseMode = "off"
	ECOPurchaseDryRun ECOPurchaseMode = "dry-run"
	ECOPurchaseLive   ECOPurchaseMode = "live"
)

type ecoPurchaseAPI interface {
	Quote(context.Context, string) (eco.MoneyFen, error)
	Balance(context.Context) (eco.MoneyFen, error)
	Buy(context.Context, string, string, eco.MoneyFen) (eco.BuyResult, error)
	OrderForItem(context.Context, string, string, eco.MoneyFen) (eco.Order, error)
}

type ECOPurchaseReceipt struct {
	NameCN, SteamPrice, OrderNo string
	Price                       eco.MoneyFen
	Ratio                       core.RatioBP
}

type ECOPurchaseService struct {
	mode       ECOPurchaseMode
	smis       *collector.SMISClient
	steam      SteamPriceSource
	store      *store.Store
	api        ecoPurchaseAPI
	candidates map[int64]catalog.Item
	delay      time.Duration
	now        func() time.Time
}

func NewECOPurchaseService(mode ECOPurchaseMode, smis *collector.SMISClient, steam SteamPriceSource, s *store.Store, api ecoPurchaseAPI, candidates []catalog.Item, delay time.Duration) (*ECOPurchaseService, error) {
	if (mode != ECOPurchaseDryRun && mode != ECOPurchaseLive) || smis == nil || steam == nil || s == nil || api == nil || len(candidates) == 0 || delay < 0 {
		return nil, errors.New("invalid ECO purchase configuration")
	}
	index := make(map[int64]catalog.Item, len(candidates))
	names := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		if c.ItemID <= 0 || c.MarketHashName == "" || c.NameCN == "" || names[c.MarketHashName] {
			return nil, errors.New("invalid or duplicate ECO candidate")
		}
		if _, ok := index[c.ItemID]; ok {
			return nil, errors.New("duplicate ECO candidate ID")
		}
		index[c.ItemID] = c
		names[c.MarketHashName] = true
	}
	return &ECOPurchaseService{mode: mode, smis: smis, steam: steam, store: s, api: api, candidates: index, delay: delay, now: time.Now}, nil
}

func (s *ECOPurchaseService) Run(ctx context.Context) (PurchaseRunResult, error) {
	var result PurchaseRunResult
	if s.mode == ECOPurchaseLive {
		if err := s.store.InitECOPurchases(ctx); err != nil {
			return result, err
		}
		rows, err := s.store.ListECOPurchases(ctx)
		if err != nil {
			return result, err
		}
		// Reconcile every ambiguous submission before permitting any new one.
		for _, p := range rows {
			if p.Status == "PLACED" && p.OrderState != nil {
				continue
			}
			if err := waitBeforeRequest(ctx, s.delay, true); err != nil {
				return result, err
			}
			order, err := s.api.OrderForItem(ctx, p.MerchantNo, p.HashName, p.Reserved)
			if err != nil {
				return result, fmt.Errorf("ECO original order unresolved; no new purchase: %w", err)
			}
			if err := s.store.SaveECOOrder(ctx, p.ID, order); err != nil {
				return result, err
			}
			result.ReconciledCount++
			if p.Kind == "AUTO" {
				ratio, err := collector.CalculateRatioBP(p.SteamPrice, order.Paid.String())
				if err != nil {
					return result, err
				}
				result.ECOPlacedPurchase = &ECOPurchaseReceipt{NameCN: p.NameCN, SteamPrice: p.SteamPrice, OrderNo: order.OrderNo, Price: order.Paid, Ratio: ratio}
			}
		}
		// A reconciliation-only cycle makes no new purchase.
		if result.ReconciledCount > 0 {
			return result, nil
		}
	}
	states, err := s.store.ListItemStatesByTier(ctx, core.TierA)
	if err != nil {
		return result, err
	}
	for _, state := range states {
		candidate, ok := s.candidates[state.ItemID]
		if !ok {
			continue
		}
		exclude := func(reason string) {
			result.ExcludedCount++
			log.Printf("ECO purchase excluded: item_id=%d reason=%s", state.ItemID, reason)
		}
		if state.MarketHashName != candidate.MarketHashName || state.NameCN != candidate.NameCN {
			exclude("catalog_identity_mismatch")
			continue
		}
		// One fen is a lower bound: handled items, unresolved orders and exhausted
		// budgets are rejected before any market request, including Steam.
		eligible, err := s.store.CanAttemptECO(ctx, state.ItemID, candidate.MarketHashName, 1, s.now())
		if err != nil {
			return result, err
		}
		if !eligible {
			exclude("handled_unresolved_or_budget")
			continue
		}
		if err := waitBeforeRequest(ctx, s.delay, true); err != nil {
			return result, err
		}
		item, err := s.smis.FetchMarketItem(ctx, state.ItemID)
		if err != nil {
			return result, err
		}
		if item.ItemID != candidate.ItemID || item.MarketHashName != candidate.MarketHashName {
			exclude("refreshed_identity_mismatch")
			continue
		}
		if item.LowestPlatform != "ECO" {
			exclude("lowest_platform_not_eco")
			continue
		}
		ceiling, err := eco.ParseMoney(item.LowestPlatformPrice)
		if err != nil || ceiling <= 0 || ceiling > eco.PerOrderLimit {
			exclude("invalid_or_above_limit_price")
			continue
		}
		if err := waitBeforeRequest(ctx, s.delay, true); err != nil {
			return result, err
		}
		quote, err := s.api.Quote(ctx, candidate.MarketHashName)
		if err != nil {
			return result, fmt.Errorf("ECO quote failed: %w", err)
		}
		if quote <= 0 || quote > ceiling {
			exclude("eco_quote_unavailable_or_increased")
			continue
		}
		eligible, err = s.store.CanAttemptECO(ctx, state.ItemID, candidate.MarketHashName, quote, s.now())
		if err != nil {
			return result, err
		}
		if !eligible {
			exclude("handled_unresolved_or_budget")
			continue
		}
		item.LowestPlatformPrice = quote.String()
		if err := waitBeforeRequest(ctx, s.delay, true); err != nil {
			return result, err
		}
		item, err = applyOfficialSteamPrice(ctx, s.steam, item)
		if err != nil {
			return result, err
		}
		tier, err := core.BaseClassify(item.MarketData)
		if err != nil {
			return result, err
		}
		if tier != core.TierA {
			exclude("not_base_a_after_steam_reprice")
			continue
		}
		result.ConfirmedCount++
		if s.mode == ECOPurchaseDryRun {
			result.DryRunCount++
			log.Printf("ECO purchase dry-run: item_id=%d market_hash_name=%q max_price=%s steam_price=%s ratio=%s", state.ItemID, candidate.MarketHashName, quote, item.SteamPrice, formatRatio(item.MarketData.Ratio))
			continue
		}
		if err := waitBeforeRequest(ctx, s.delay, true); err != nil {
			return result, err
		}
		balance, err := s.api.Balance(ctx)
		if err != nil {
			return result, err
		}
		if balance < quote {
			exclude("insufficient_balance")
			continue
		}
		if err := waitBeforeRequest(ctx, s.delay, true); err != nil {
			return result, err
		}
		now := s.now() // Reservation day is determined immediately before submission.
		merchantNo, err := generateC5TradeNo(now)
		if err != nil {
			return result, err
		}
		p, err := s.store.ReserveECOPurchase(ctx, store.ECOReservation{ItemID: state.ItemID, HashName: candidate.MarketHashName, NameCN: candidate.NameCN, MerchantNo: merchantNo, SteamPrice: item.SteamPrice, Ceiling: quote, Kind: "AUTO", At: now})
		if errors.Is(err, store.ErrECOPurchaseBlocked) {
			exclude("reservation_blocked")
			continue
		}
		if err != nil {
			return result, err
		}
		bought, err := s.api.Buy(ctx, candidate.MarketHashName, merchantNo, quote)
		if err == nil && (bought.Paid <= 0 || bought.Paid > quote || bought.MerchantNo != merchantNo || bought.OrderNo == "") {
			err = errors.New("invalid ECO purchase receipt")
		}
		if err != nil {
			saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			markErr := s.store.MarkECOUncertain(saveCtx, p.ID)
			cancel()
			if markErr != nil {
				return result, fmt.Errorf("ECO uncertain submission and persistence failed: %v; %w", markErr, err)
			}
			return result, fmt.Errorf("ECO submission uncertain; never resubmit: %w", err)
		}
		if err := s.store.MarkECOPlaced(ctx, p.ID, bought); err != nil {
			return result, err
		}
		result.PlacedCount++
		if err := waitBeforeRequest(ctx, s.delay, true); err != nil {
			return result, err
		}
		order, err := s.api.OrderForItem(ctx, merchantNo, candidate.MarketHashName, quote)
		if err != nil {
			return result, fmt.Errorf("ECO order placed but detail unverified: %w", err)
		}
		if err := s.store.SaveECOOrder(ctx, p.ID, order); err != nil {
			return result, err
		}
		ratio, err := collector.CalculateRatioBP(item.SteamPrice, order.Paid.String())
		if err != nil {
			return result, err
		}
		result.ECOPlacedPurchase = &ECOPurchaseReceipt{NameCN: candidate.NameCN, SteamPrice: item.SteamPrice, OrderNo: order.OrderNo, Price: order.Paid, Ratio: ratio}
		return result, nil // Preserve the existing one-success-per-run runtime boundary.
	}
	return result, nil
}
