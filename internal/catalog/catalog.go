package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const Version = 1

var RequiredCriteria = Criteria{
	AppID:    730,
	Quality:  "Normal",
	Exterior: "Field-Tested",
	Rarity:   "Mil-Spec Grade",
}

type Criteria struct {
	AppID    int    `json:"app_id"`
	Quality  string `json:"quality"`
	Exterior string `json:"exterior"`
	Rarity   string `json:"rarity"`
}

type Item struct {
	ItemID         int64  `json:"item_id"`
	MarketHashName string `json:"market_hash_name"`
	NameCN         string `json:"name_cn"`
}

type Catalog struct {
	Version     int       `json:"version"`
	GeneratedAt time.Time `json:"generated_at"`
	Criteria    Criteria  `json:"criteria"`
	Items       []Item    `json:"items"`
}

func Load(path string) (Catalog, error) {
	if strings.TrimSpace(path) == "" {
		return Catalog{}, fmt.Errorf("candidate catalog path cannot be empty")
	}
	file, err := os.Open(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("open candidate catalog %s: %w", path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var value Catalog
	if err := decoder.Decode(&value); err != nil {
		return Catalog{}, fmt.Errorf("decode candidate catalog %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Catalog{}, fmt.Errorf("decode candidate catalog %s: multiple JSON values", path)
		}
		return Catalog{}, fmt.Errorf("decode candidate catalog %s: %w", path, err)
	}
	if err := Validate(value); err != nil {
		return Catalog{}, fmt.Errorf("validate candidate catalog %s: %w", path, err)
	}
	return value, nil
}

func SaveAtomic(path string, value Catalog) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("candidate catalog path cannot be empty")
	}
	if err := Validate(value); err != nil {
		return fmt.Errorf("validate candidate catalog: %w", err)
	}

	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".candidate-catalog-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary candidate catalog beside %s: %w", path, err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode candidate catalog: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set candidate catalog permissions: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync candidate catalog: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close candidate catalog: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace candidate catalog %s: %w", path, err)
	}
	keepTemporary = false
	return nil
}

func Validate(value Catalog) error {
	if value.Version != Version {
		return fmt.Errorf("version is %d, want %d", value.Version, Version)
	}
	if value.GeneratedAt.IsZero() || value.GeneratedAt.UTC().UnixMilli() < 0 {
		return fmt.Errorf("generated_at must be on or after the Unix epoch")
	}
	if value.Criteria != RequiredCriteria {
		return fmt.Errorf("criteria are %#v, want %#v", value.Criteria, RequiredCriteria)
	}
	if len(value.Items) == 0 {
		return fmt.Errorf("items cannot be empty")
	}
	seenNames := make(map[string]struct{}, len(value.Items))
	var previousID int64
	for index, item := range value.Items {
		if item.ItemID <= 0 {
			return fmt.Errorf("item %d has non-positive item_id", index)
		}
		if index > 0 && item.ItemID <= previousID {
			return fmt.Errorf("items must be sorted by unique item_id")
		}
		if strings.TrimSpace(item.MarketHashName) == "" {
			return fmt.Errorf("item %d has empty market_hash_name", item.ItemID)
		}
		if strings.TrimSpace(item.NameCN) == "" {
			return fmt.Errorf("item %d has empty name_cn", item.ItemID)
		}
		if _, exists := seenNames[item.MarketHashName]; exists {
			return fmt.Errorf("duplicate market_hash_name %q", item.MarketHashName)
		}
		seenNames[item.MarketHashName] = struct{}{}
		previousID = item.ItemID
	}
	return nil
}
