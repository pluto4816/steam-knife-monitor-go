package core

import (
	"testing"
	"time"
)

func TestCheckInterval(t *testing.T) {
	tests := []struct {
		name     string
		tier     Tier
		fastDrop bool
		want     time.Duration
	}{
		{"A", TierA, false, 5 * time.Minute},
		{"B1", TierB1, false, 10 * time.Minute},
		{"B2", TierB2, false, 30 * time.Minute},
		{"B3", TierB3, false, 60 * time.Minute},
		{"C", TierC, false, 240 * time.Minute},
		{"fast A stays at minimum", TierA, true, 5 * time.Minute},
		{"fast B1", TierB1, true, 5 * time.Minute},
		{"fast B2", TierB2, true, 15 * time.Minute},
		{"fast B3", TierB3, true, 30 * time.Minute},
		{"fast C", TierC, true, 120 * time.Minute},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := CheckInterval(testCase.tier, testCase.fastDrop)
			if err != nil {
				t.Fatal(err)
			}
			if got != testCase.want {
				t.Fatalf("interval = %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestCheckIntervalRejectsInvalidTier(t *testing.T) {
	if _, err := CheckInterval(Tier("UNKNOWN"), false); err == nil {
		t.Fatal("invalid tier was accepted")
	}
}
