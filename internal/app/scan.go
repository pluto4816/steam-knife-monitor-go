package app

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

const smisUpdateLayout = "2006-01-02 15:04:05"

type ScanResult struct {
	CandidateCount int
	ResumedCount   int
	MatchedCount   int
	ProcessedCount int
	DuplicateCount int
}

func RunScanCycle(
	ctx context.Context,
	client *collector.SMISClient,
	stateStore *store.Store,
	pageDelay time.Duration,
) (ScanResult, error) {
	result := ScanResult{}
	candidates := make(map[int64]collector.Candidate)
	requestMade := false

	for pageNumber := 1; ; pageNumber++ {
		if err := waitBeforeRequest(ctx, pageDelay, requestMade); err != nil {
			result.CandidateCount = len(candidates)
			return result, fmt.Errorf("wait before candidate page %d: %w", pageNumber, err)
		}
		page, err := client.FetchCandidatePage(ctx, pageNumber)
		requestMade = true
		if err != nil {
			result.CandidateCount = len(candidates)
			return result, fmt.Errorf("fetch candidate page %d: %w", pageNumber, err)
		}
		if page.RawCount == 0 {
			break
		}
		for _, candidate := range page.Items {
			if _, exists := candidates[candidate.ItemID]; !exists {
				candidates[candidate.ItemID] = candidate
			}
		}
	}

	result.CandidateCount = len(candidates)
	if result.CandidateCount == 0 {
		return result, nil
	}

	marketItems := make(map[int64]collector.MarketItem)
	var exchangeErr error
	for pageNumber := 1; ; pageNumber++ {
		if err := waitBeforeRequest(ctx, pageDelay, requestMade); err != nil {
			exchangeErr = fmt.Errorf("exchange scan incomplete waiting for page %d: %w", pageNumber, err)
			break
		}
		page, err := client.FetchExchangePage(ctx, pageNumber)
		requestMade = true
		if err != nil {
			exchangeErr = fmt.Errorf("exchange scan incomplete at page %d: %w", pageNumber, err)
			break
		}
		if page.RawCount == 0 {
			break
		}
		for _, item := range page.Items {
			if _, allowed := candidates[item.ItemID]; !allowed {
				continue
			}
			current, exists := marketItems[item.ItemID]
			if !exists {
				marketItems[item.ItemID] = item
				continue
			}
			result.DuplicateCount++
			if hasNewerUpdateTime(current.UpdatedAt, item.UpdatedAt) {
				marketItems[item.ItemID] = item
			}
		}
	}

	result.MatchedCount = len(marketItems)
	itemIDs := make([]int64, 0, len(marketItems))
	for itemID := range marketItems {
		itemIDs = append(itemIDs, itemID)
	}
	sort.Slice(itemIDs, func(left, right int) bool {
		return itemIDs[left] < itemIDs[right]
	})

	for _, itemID := range itemIDs {
		checkedAt := time.Now().UTC()
		if _, err := ProcessMarketItem(ctx, stateStore, marketItems[itemID], checkedAt); err != nil {
			if exchangeErr != nil {
				return result, fmt.Errorf("%v; process item %d: %w", exchangeErr, itemID, err)
			}
			return result, fmt.Errorf("process item %d: %w", itemID, err)
		}
		result.ProcessedCount++
	}
	if exchangeErr != nil {
		return result, exchangeErr
	}
	return result, nil
}

func waitBeforeRequest(ctx context.Context, delay time.Duration, requestMade bool) error {
	if !requestMade || delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func hasNewerUpdateTime(current, candidate string) bool {
	currentTime, currentErr := time.Parse(smisUpdateLayout, current)
	candidateTime, candidateErr := time.Parse(smisUpdateLayout, candidate)
	if candidateErr != nil {
		return false
	}
	if currentErr != nil {
		return true
	}
	return candidateTime.After(currentTime)
}
