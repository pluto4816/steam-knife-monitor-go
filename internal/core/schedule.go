package core

import (
	"fmt"
	"time"
)

func CheckInterval(tier Tier, fastDrop bool) (time.Duration, error) {
	var interval time.Duration
	switch tier {
	case TierA:
		interval = 5 * time.Minute
	case TierB1:
		interval = 10 * time.Minute
	case TierB2:
		interval = 30 * time.Minute
	case TierB3:
		interval = 60 * time.Minute
	case TierC:
		interval = 240 * time.Minute
	default:
		return 0, fmt.Errorf("invalid tier %q", tier)
	}
	if fastDrop && interval > 5*time.Minute {
		interval /= 2
	}
	return interval, nil
}
