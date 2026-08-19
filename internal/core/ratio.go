package core

import (
	"fmt"
	"strconv"
	"strings"
)

const ratioScale int64 = 10000
const maxRatioBP int64 = 1<<31 - 1

// ParseRatio converts a plain non-negative decimal string to RatioBP exactly.
// A json.Number should be passed through its String method. Non-zero precision
// beyond four fractional digits is rejected instead of rounded.
func ParseRatio(raw string) (RatioBP, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return 0, invalidRatio(raw)
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" || !decimalDigits(parts[0]) {
		return 0, invalidRatio(raw)
	}

	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || !decimalDigits(fraction) {
			return 0, invalidRatio(raw)
		}
	}
	if len(fraction) > 4 {
		if strings.Trim(fraction[4:], "0") != "" {
			return 0, fmt.Errorf("invalid ratio %q: more than four decimal places", raw)
		}
		fraction = fraction[:4]
	}
	fraction += strings.Repeat("0", 4-len(fraction))

	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > maxRatioBP/ratioScale {
		return 0, fmt.Errorf("invalid ratio %q: out of range", raw)
	}
	fractionValue, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, invalidRatio(raw)
	}
	scaled := whole*ratioScale + fractionValue
	if scaled > maxRatioBP {
		return 0, fmt.Errorf("invalid ratio %q: out of range", raw)
	}
	return RatioBP(scaled), nil
}

func decimalDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}

func invalidRatio(raw string) error {
	return fmt.Errorf("invalid ratio %q: expected a non-negative plain decimal", raw)
}
