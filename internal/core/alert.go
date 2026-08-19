package core

import "fmt"

type AlertKind string

const (
	AlertKindAEntry       AlertKind = "A_ENTRY"
	AlertKindAImprovement AlertKind = "A_IMPROVEMENT"

	AlertImprovementThreshold RatioBP = 200
)

// DecideAlert applies the A-entry and continuous-A improvement rules.
func DecideAlert(
	previous *Tier,
	current Tier,
	currentRatio RatioBP,
	hasPending bool,
	lastSuccessfulRatio *RatioBP,
) (AlertKind, bool, error) {
	if !validTier(current) {
		return "", false, fmt.Errorf("invalid current tier %q", current)
	}
	if previous != nil && !validTier(*previous) {
		return "", false, fmt.Errorf("invalid previous tier %q", *previous)
	}
	if currentRatio < 0 {
		return "", false, fmt.Errorf("invalid current ratio: value is negative")
	}
	if lastSuccessfulRatio != nil && *lastSuccessfulRatio < 0 {
		return "", false, fmt.Errorf("invalid last successful ratio: value is negative")
	}

	if current != TierA {
		return "", false, nil
	}
	if previous == nil || *previous != TierA {
		return AlertKindAEntry, true, nil
	}
	if hasPending || lastSuccessfulRatio == nil {
		return "", false, nil
	}

	improvement, err := RatioDrop(*lastSuccessfulRatio, currentRatio)
	if err != nil {
		return "", false, err
	}
	if improvement >= AlertImprovementThreshold {
		return AlertKindAImprovement, true, nil
	}
	return "", false, nil
}
