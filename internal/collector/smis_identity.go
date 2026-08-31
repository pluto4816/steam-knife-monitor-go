package collector

import (
	"context"
	"fmt"
)

const (
	requiredRarity   = "军规级"
	requiredQuality  = "普通"
	requiredExterior = "久经沙场"
)

type CandidateIdentity struct {
	ItemID         int64
	MarketHashName string
	NameCN         string
}

func (client *SMISClient) FetchCandidateIdentity(ctx context.Context, itemID int64) (CandidateIdentity, error) {
	if itemID <= 0 {
		return CandidateIdentity{}, fmt.Errorf("item ID must be positive")
	}
	path := fmt.Sprintf(detailPath, itemID)
	data, err := client.get(ctx, path)
	if err != nil {
		return CandidateIdentity{}, err
	}
	record, ok := data.(map[string]any)
	if !ok {
		return CandidateIdentity{}, fmt.Errorf("detail response data must be an object")
	}
	responseID, ok := positiveID(record["id"])
	if !ok {
		return CandidateIdentity{}, fmt.Errorf("detail response has invalid id")
	}
	if responseID != itemID {
		return CandidateIdentity{}, fmt.Errorf("detail response ID %d does not match requested ID %d", responseID, itemID)
	}
	hashName := stringValue(record["hashName"])
	if hashName == "" {
		return CandidateIdentity{}, fmt.Errorf("detail item %d has empty hashName", itemID)
	}
	nameCN := stringValue(record["cnName"])
	if nameCN == "" {
		return CandidateIdentity{}, fmt.Errorf("detail item %d has empty cnName", itemID)
	}
	if rarity := stringValue(record["rarity"]); rarity != requiredRarity {
		return CandidateIdentity{}, fmt.Errorf("detail item %d rarity is %q, want %q", itemID, rarity, requiredRarity)
	}
	if quality := stringValue(record["quality"]); quality != requiredQuality {
		return CandidateIdentity{}, fmt.Errorf("detail item %d quality is %q, want %q", itemID, quality, requiredQuality)
	}
	if exterior := stringValue(record["exterior"]); exterior != requiredExterior {
		return CandidateIdentity{}, fmt.Errorf("detail item %d exterior is %q, want %q", itemID, exterior, requiredExterior)
	}
	return CandidateIdentity{ItemID: itemID, MarketHashName: hashName, NameCN: nameCN}, nil
}
