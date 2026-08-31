package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveAtomicAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidates.json")
	want := validCatalog()
	if err := SaveAtomic(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != want.Version || !got.GeneratedAt.Equal(want.GeneratedAt) || got.Criteria != want.Criteria || len(got.Items) != 2 {
		t.Fatalf("catalog = %#v, want %#v", got, want)
	}
	if got.Items[0] != want.Items[0] || got.Items[1] != want.Items[1] {
		t.Fatalf("items = %#v, want %#v", got.Items, want.Items)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %o, want 644", info.Mode().Perm())
	}
}

func TestSaveAtomicInvalidCatalogPreservesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidates.json")
	if err := os.WriteFile(path, []byte("old catalog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := validCatalog()
	invalid.Items = nil
	if err := SaveAtomic(path, invalid); err == nil {
		t.Fatal("invalid catalog was accepted")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old catalog\n" {
		t.Fatalf("existing file changed to %q", content)
	}
}

func TestLoadRejectsUnknownAndInvalidData(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"unknown field", `{"version":1,"unknown":true}`, "unknown field"},
		{"wrong version", `{"version":2}`, "version is 2"},
		{"empty items", `{"version":1,"generated_at":"2026-08-24T00:00:00Z","criteria":{"app_id":730,"quality":"Normal","exterior":"Field-Tested","rarity":"Mil-Spec Grade"},"items":[]}`, "items cannot be empty"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "candidates.json")
			if err := os.WriteFile(path, []byte(testCase.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestValidateRequiresSortedUniqueCompleteItems(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Catalog)
	}{
		{"unsorted", func(value *Catalog) { value.Items[0], value.Items[1] = value.Items[1], value.Items[0] }},
		{"duplicate name", func(value *Catalog) { value.Items[1].MarketHashName = value.Items[0].MarketHashName }},
		{"empty Chinese name", func(value *Catalog) { value.Items[0].NameCN = "" }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			value := validCatalog()
			testCase.change(&value)
			if err := Validate(value); err == nil {
				t.Fatal("invalid catalog was accepted")
			}
		})
	}
}

func validCatalog() Catalog {
	return Catalog{
		Version:     Version,
		GeneratedAt: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC),
		Criteria:    RequiredCriteria,
		Items: []Item{
			{ItemID: 1, MarketHashName: "AK-47 | One (Field-Tested)", NameCN: "一"},
			{ItemID: 2, MarketHashName: "AK-47 | Two (Field-Tested)", NameCN: "二"},
		},
	}
}
