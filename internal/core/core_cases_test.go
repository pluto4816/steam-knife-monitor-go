package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type coreCases struct {
	Classification []classificationCase
	Trend          []trendCase
}

type classificationCase struct {
	Name     string
	Previous *string
	Ratio    string
	Volume   int
	Want     string
}

type trendCase struct {
	Name     string
	Previous string
	Current  string
	Drop     int32
	Fast     bool
}

func TestCoreAcceptanceCases(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "core_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases coreCases
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}

	for _, testCase := range cases.Classification {
		testCase := testCase
		t.Run("classify/"+testCase.Name, func(t *testing.T) {
			ratio, err := ParseRatio(testCase.Ratio)
			if err != nil {
				t.Fatal(err)
			}
			var previous *Tier
			if testCase.Previous != nil {
				value := Tier(*testCase.Previous)
				previous = &value
			}
			change, err := ResolveTier(previous, MarketData{
				Ratio:            ratio,
				SteamDailyVolume: testCase.Volume,
			})
			if err != nil {
				t.Fatal(err)
			}
			if change.Current != Tier(testCase.Want) {
				t.Fatalf("got %s, want %s", change.Current, testCase.Want)
			}
		})
	}

	for _, testCase := range cases.Trend {
		testCase := testCase
		t.Run("trend/"+testCase.Name, func(t *testing.T) {
			previous, err := ParseRatio(testCase.Previous)
			if err != nil {
				t.Fatal(err)
			}
			current, err := ParseRatio(testCase.Current)
			if err != nil {
				t.Fatal(err)
			}
			drop, err := RatioDrop(previous, current)
			if err != nil {
				t.Fatal(err)
			}
			if drop != RatioBP(testCase.Drop) {
				t.Fatalf("drop = %d, want %d", drop, testCase.Drop)
			}
			if IsFastDrop(drop) != testCase.Fast {
				t.Fatalf("fast drop = %t, want %t", IsFastDrop(drop), testCase.Fast)
			}
		})
	}
}
