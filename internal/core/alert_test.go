package core

import "testing"

func TestDecideAlert(t *testing.T) {
	tests := []struct {
		name                string
		previous            *Tier
		current             Tier
		currentRatio        RatioBP
		hasPending          bool
		lastSuccessfulRatio *RatioBP
		wantKind            AlertKind
		wantAlert           bool
	}{
		{name: "first observation in A", current: TierA, currentRatio: 5900, wantKind: AlertKindAEntry, wantAlert: true},
		{name: "non-A enters A", previous: tierPointer(TierB1), current: TierA, currentRatio: 5900, wantKind: AlertKindAEntry, wantAlert: true},
		{name: "A leaves", previous: tierPointer(TierA), current: TierB1, currentRatio: 6200},
		{name: "improvement below threshold", previous: tierPointer(TierA), current: TierA, currentRatio: 5801, lastSuccessfulRatio: ratioPointer(6000)},
		{name: "improvement at threshold", previous: tierPointer(TierA), current: TierA, currentRatio: 5800, lastSuccessfulRatio: ratioPointer(6000), wantKind: AlertKindAImprovement, wantAlert: true},
		{name: "pending suppresses improvement", previous: tierPointer(TierA), current: TierA, currentRatio: 5700, hasPending: true, lastSuccessfulRatio: ratioPointer(6000)},
		{name: "missing baseline suppresses improvement", previous: tierPointer(TierA), current: TierA, currentRatio: 5700},
		{name: "ratio did not improve", previous: tierPointer(TierA), current: TierA, currentRatio: 6100, lastSuccessfulRatio: ratioPointer(6000)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, alert, err := DecideAlert(
				test.previous,
				test.current,
				test.currentRatio,
				test.hasPending,
				test.lastSuccessfulRatio,
			)
			if err != nil {
				t.Fatalf("DecideAlert() error = %v", err)
			}
			if kind != test.wantKind || alert != test.wantAlert {
				t.Fatalf("DecideAlert() = (%q, %v), want (%q, %v)", kind, alert, test.wantKind, test.wantAlert)
			}
		})
	}
}

func TestDecideAlertRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name                string
		previous            *Tier
		current             Tier
		currentRatio        RatioBP
		lastSuccessfulRatio *RatioBP
	}{
		{name: "current tier", current: Tier("invalid")},
		{name: "previous tier", previous: tierPointer(Tier("invalid")), current: TierA},
		{name: "current ratio", current: TierA, currentRatio: -1},
		{name: "baseline ratio", previous: tierPointer(TierA), current: TierA, lastSuccessfulRatio: ratioPointer(-1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := DecideAlert(test.previous, test.current, test.currentRatio, false, test.lastSuccessfulRatio); err == nil {
				t.Fatal("DecideAlert() error = nil, want error")
			}
		})
	}
}

func tierPointer(value Tier) *Tier {
	return &value
}

func ratioPointer(value RatioBP) *RatioBP {
	return &value
}
