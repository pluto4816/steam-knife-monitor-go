package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
)

type CandidateUpdateResult struct {
	DefinitionRecordCount    int
	DefinitionCandidateCount int
	DefinitionSnapshotSHA256 string
	SMISCandidateCount       int
	MatchedCount             int
	ReusedCount              int
	DetailFetchedCount       int
	DefinitionOnlyCount      int
	DefinitionOnlyNames      []string
	SMISOnlyCount            int
}

func RunCandidateUpdate(
	ctx context.Context,
	definitionsClient *collector.CS2DefinitionsClient,
	smisClient *collector.SMISClient,
	catalogPath string,
	pageDelay time.Duration,
	detailDelay time.Duration,
) (CandidateUpdateResult, error) {
	var result CandidateUpdateResult
	if definitionsClient == nil {
		return result, fmt.Errorf("CS2 definitions client cannot be nil")
	}
	if smisClient == nil {
		return result, fmt.Errorf("smis client cannot be nil")
	}
	if strings.TrimSpace(catalogPath) == "" {
		return result, fmt.Errorf("candidate catalog path cannot be empty")
	}
	if pageDelay < 0 || detailDelay < 0 {
		return result, fmt.Errorf("candidate update delays cannot be negative")
	}

	var previous catalog.Catalog
	previousExists := false
	loaded, err := catalog.Load(catalogPath)
	if err == nil {
		previous = loaded
		previousExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("load previous candidate catalog: %w", err)
	}

	snapshot, err := definitionsClient.FetchCandidateSnapshot(ctx)
	if err != nil {
		return result, fmt.Errorf("collect CS2 definition candidates: %w", err)
	}
	result.DefinitionRecordCount = snapshot.RecordCount
	result.DefinitionCandidateCount = len(snapshot.Candidates)
	result.DefinitionSnapshotSHA256 = snapshot.SHA256
	definitionNames := make(map[string]struct{}, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		definitionNames[candidate.MarketHashName] = struct{}{}
	}

	smisCandidates, err := fetchAllSMISCandidates(ctx, smisClient, pageDelay)
	if err != nil {
		return result, fmt.Errorf("collect smis candidates: %w", err)
	}
	result.SMISCandidateCount = len(smisCandidates)
	if result.SMISCandidateCount == 0 {
		return result, fmt.Errorf("smis candidate search returned no candidates")
	}

	cachedByID := make(map[int64]catalog.Item)
	if previousExists {
		for _, item := range previous.Items {
			cachedByID[item.ItemID] = item
		}
	}

	items := make([]catalog.Item, 0, len(smisCandidates))
	matchedNames := make(map[string]struct{})
	for _, candidate := range smisCandidates {
		cached, cachedExists := cachedByID[candidate.ItemID]
		if cachedExists {
			if _, stillCandidate := definitionNames[cached.MarketHashName]; stillCandidate {
				nameCN := candidate.NameCN
				if strings.TrimSpace(nameCN) == "" {
					nameCN = cached.NameCN
				}
				items = append(items, catalog.Item{
					ItemID:         candidate.ItemID,
					MarketHashName: cached.MarketHashName,
					NameCN:         nameCN,
				})
				matchedNames[cached.MarketHashName] = struct{}{}
				result.ReusedCount++
				continue
			}
		}

		if err := waitBeforeRequest(ctx, detailDelay, true); err != nil {
			return result, fmt.Errorf("wait before smis identity item %d: %w", candidate.ItemID, err)
		}
		identity, err := smisClient.FetchCandidateIdentity(ctx, candidate.ItemID)
		if err != nil {
			return result, fmt.Errorf("fetch smis identity item %d: %w", candidate.ItemID, err)
		}
		result.DetailFetchedCount++
		if _, exists := definitionNames[identity.MarketHashName]; !exists {
			continue
		}
		if _, exists := matchedNames[identity.MarketHashName]; exists {
			return result, fmt.Errorf("multiple smis IDs map to CS2 definition candidate %q", identity.MarketHashName)
		}
		items = append(items, catalog.Item{
			ItemID:         identity.ItemID,
			MarketHashName: identity.MarketHashName,
			NameCN:         identity.NameCN,
		})
		matchedNames[identity.MarketHashName] = struct{}{}
	}

	sort.Slice(items, func(left, right int) bool {
		return items[left].ItemID < items[right].ItemID
	})
	result.MatchedCount = len(items)
	for definitionName := range definitionNames {
		if _, matched := matchedNames[definitionName]; !matched {
			result.DefinitionOnlyNames = append(result.DefinitionOnlyNames, definitionName)
		}
	}
	sort.Strings(result.DefinitionOnlyNames)
	result.DefinitionOnlyCount = len(result.DefinitionOnlyNames)
	result.SMISOnlyCount = result.SMISCandidateCount - result.MatchedCount
	if result.MatchedCount == 0 {
		return result, fmt.Errorf("CS2 definition and smis candidate sets have no mapped items")
	}

	updated := catalog.Catalog{
		Version:     catalog.Version,
		GeneratedAt: time.Now().UTC(),
		Criteria:    catalog.RequiredCriteria,
		Items:       items,
	}
	if err := catalog.SaveAtomic(catalogPath, updated); err != nil {
		return result, err
	}
	return result, nil
}

func fetchAllSMISCandidates(
	ctx context.Context,
	client *collector.SMISClient,
	requestDelay time.Duration,
) ([]collector.Candidate, error) {
	byID := make(map[int64]collector.Candidate)
	requestMade := false
	for pageNumber := 1; ; pageNumber++ {
		if err := waitBeforeRequest(ctx, requestDelay, requestMade); err != nil {
			return nil, fmt.Errorf("wait before smis candidate page %d: %w", pageNumber, err)
		}
		page, err := client.FetchCandidatePage(ctx, pageNumber)
		requestMade = true
		if err != nil {
			return nil, fmt.Errorf("fetch smis candidate page %d: %w", pageNumber, err)
		}
		if page.PageNum != int64(pageNumber) {
			return nil, fmt.Errorf(
				"smis candidate response pageNum %d does not match request %d",
				page.PageNum,
				pageNumber,
			)
		}
		if page.PageSize != int64(collector.SMISPageSize) {
			return nil, fmt.Errorf(
				"smis candidate response pageSize %d, want %d",
				page.PageSize,
				collector.SMISPageSize,
			)
		}
		if page.RawCount > collector.SMISPageSize {
			return nil, fmt.Errorf(
				"smis candidate page %d contains %d items, exceeds pageSize %d",
				pageNumber, page.RawCount, collector.SMISPageSize,
			)
		}
		if page.RawCount == 0 {
			break
		}
		if len(page.Items) != page.RawCount {
			return nil, fmt.Errorf(
				"smis candidate page %d parsed %d of %d items",
				pageNumber,
				len(page.Items),
				page.RawCount,
			)
		}
		newCount := 0
		for _, candidate := range page.Items {
			if _, exists := byID[candidate.ItemID]; !exists {
				byID[candidate.ItemID] = candidate
				newCount++
			}
		}
		if newCount == 0 {
			return nil, fmt.Errorf("smis candidate page %d made no progress", pageNumber)
		}
	}
	items := make([]collector.Candidate, 0, len(byID))
	for _, item := range byID {
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].ItemID < items[right].ItemID
	})
	return items, nil
}
