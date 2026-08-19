package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseRatio(t *testing.T) {
	valid := map[string]RatioBP{
		"0.6":     6000,
		"0.60":    6000,
		"0.6000":  6000,
		"0.5987":  5987,
		"0.6571":  6571,
		"0.75":    7500,
		"1.0000":  10000,
		"0.60000": 6000,
	}
	for input, want := range valid {
		got, err := ParseRatio(input)
		if err != nil {
			t.Errorf("ParseRatio(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("ParseRatio(%q) = %d, want %d", input, got, want)
		}
	}

	invalid := []string{
		"",
		"-0.1",
		"NaN",
		"Infinity",
		"abc",
		"0.1.2",
		"0.12345",
		"6e-1",
		" 0.6",
	}
	for _, input := range invalid {
		if _, err := ParseRatio(input); err == nil {
			t.Errorf("ParseRatio(%q) unexpectedly succeeded", input)
		}
	}
}

func TestJSONNumberReachesParserWithoutFloat(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(`{"ratio":0.5987}`))
	decoder.UseNumber()
	var payload struct {
		Ratio json.Number
	}
	if err := decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	got, err := ParseRatio(payload.Ratio.String())
	if err != nil {
		t.Fatal(err)
	}
	if got != 5987 {
		t.Fatalf("got %d, want 5987", got)
	}
}

func TestCoreRejectsInvalidConstructedValues(t *testing.T) {
	if _, err := BaseClassify(MarketData{Ratio: -1, SteamDailyVolume: 350}); err == nil {
		t.Error("negative ratio was accepted")
	}
	if _, err := BaseClassify(MarketData{Ratio: 6000, SteamDailyVolume: -1}); err == nil {
		t.Error("negative volume was accepted")
	}
	invalid := Tier("UNKNOWN")
	if _, err := ResolveTier(&invalid, MarketData{Ratio: 6000, SteamDailyVolume: 350}); err == nil {
		t.Error("invalid previous tier was accepted")
	}
}

func TestTierChangeChanged(t *testing.T) {
	initial := TierChange{Current: TierA}
	if !initial.Changed() {
		t.Error("initial classification must be a change")
	}
	previous := TierA
	unchanged := TierChange{Previous: &previous, Current: TierA}
	if unchanged.Changed() {
		t.Error("equal tiers must not be a change")
	}
}

func TestRatioDropRejectsNegativeInput(t *testing.T) {
	if _, err := RatioDrop(-1, 6000); err == nil {
		t.Error("negative previous ratio was accepted")
	}
	if _, err := RatioDrop(6000, -1); err == nil {
		t.Error("negative current ratio was accepted")
	}
}
