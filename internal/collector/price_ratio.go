package collector

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

func CalculateRatioBP(steamPrice, platformPrice string) (core.RatioBP, error) {
	steam, err := parsePositiveDecimalPrice(steamPrice, "Steam")
	if err != nil {
		return 0, err
	}
	platform, err := parsePositiveDecimalPrice(platformPrice, "platform")
	if err != nil {
		return 0, err
	}
	ratio, err := detailRatioBP([]*big.Rat{steam, platform})
	if err != nil {
		return 0, fmt.Errorf("calculate price ratio: %w", err)
	}
	return ratio, nil
}

func parsePositiveDecimalPrice(value, label string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("%s price is empty", label)
	}
	seenDot := false
	digitCount := 0
	for _, character := range value {
		switch {
		case character >= '0' && character <= '9':
			digitCount++
		case character == '.' && !seenDot:
			seenDot = true
		default:
			return nil, fmt.Errorf("%s price %q is not a plain decimal", label, value)
		}
	}
	if digitCount == 0 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return nil, fmt.Errorf("%s price %q is not a plain decimal", label, value)
	}
	price, ok := new(big.Rat).SetString(value)
	if !ok || price.Sign() <= 0 {
		return nil, fmt.Errorf("%s price must be positive", label)
	}
	return price, nil
}
