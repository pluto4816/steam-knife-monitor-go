package eco

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MoneyFen is an exact, non-negative CNY amount in fen.
type MoneyFen int64

func (m MoneyFen) String() string { return fmt.Sprintf("%d.%02d", m/100, m%100) }

func ParseMoney(s string) (MoneyFen, error) {
	bad := errors.New("invalid ECO CNY amount")
	p := strings.Split(s, ".")
	if len(p) > 2 || len(p[0]) == 0 || len(p[0]) > 12 {
		return 0, bad
	}
	for _, part := range p {
		if part == "" {
			return 0, bad
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, bad
			}
		}
	}
	frac := "00"
	if len(p) == 2 {
		digits := strings.TrimRight(p[1], "0")
		if len(digits) > 2 {
			return 0, bad
		}
		frac = (digits + "00")[:2]
	}
	yuan, err := strconv.ParseInt(p[0], 10, 64)
	if err != nil {
		return 0, bad
	}
	fen, _ := strconv.ParseInt(frac, 10, 64)
	return MoneyFen(yuan*100 + fen), nil
}

func decodeMoney(raw json.RawMessage) (MoneyFen, error) {
	s := string(raw)
	if len(s) > 0 && s[0] == '"' {
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, errors.New("invalid ECO money field")
		}
	}
	return ParseMoney(s)
}
