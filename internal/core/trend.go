package core

import "fmt"

const FastDropThreshold RatioBP = 300

func RatioDrop(previous, current RatioBP) (RatioBP, error) {
	if previous < 0 || current < 0 {
		return 0, fmt.Errorf("ratios must be non-negative")
	}
	return previous - current, nil
}

func IsFastDrop(drop RatioBP) bool {
	return drop >= FastDropThreshold
}
