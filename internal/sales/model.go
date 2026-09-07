// Package sales provides the manually maintained selling assistant. It never trades.
package sales

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

const MaxQuantity int64 = 100000
const MaxMoney int64 = 1000000000 // fen; bounds also keep intermediate products in int64.

func ParseMoney(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("金额须为最多两位小数的非负数")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || len(fraction) > 2 {
			return 0, fmt.Errorf("金额最多两位小数")
		}
	}
	for _, c := range parts[0] + fraction {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("金额格式不正确")
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > MaxMoney/100 {
		return 0, fmt.Errorf("金额超出范围")
	}
	fraction += strings.Repeat("0", 2-len(fraction))
	cents, _ := strconv.ParseInt(fraction, 10, 64)
	value := whole*100 + cents
	if value > MaxMoney {
		return 0, fmt.Errorf("金额超出范围")
	}
	return value, nil
}
func Money(fen int64) string      { return fmt.Sprintf("%d.%02d", fen/100, fen%100) }
func Ratio(r core.RatioBP) string { return fmt.Sprintf("%d.%04d", r/10000, r%10000) }
func rounded(n, d int64) int64 {
	q, r := n/d, n%d
	if r*2 > d {
		q++
	}
	return q
}
func ceil(n, d int64) int64 { return (n + d - 1) / d }

// Discount uses cost / (Steam gross / 1.15), with no fee floor or intermediate rounding.
func Discount(cost, gross int64) (core.RatioBP, error) {
	if cost <= 0 || gross <= 0 || cost > MaxMoney || gross > MaxMoney {
		return 0, fmt.Errorf("买入单价和 Steam 售价必须为正数")
	}
	r := rounded(cost*11500, gross)
	if r > 2147483647 {
		return 0, fmt.Errorf("挂刀比例超出范围")
	}
	return core.RatioBP(r), nil
}

type Lot struct {
	RefreshQuote                       bool
	ID                                 int64
	Name, HashName, Platform, BoughtOn string
	Quantity, UnitCost, Sold, Quote    int64
	Target                             core.RatioBP
	Status, QuoteAt                    string
	Version                            int64
}

func (l Lot) Remaining() int64    { return l.Quantity - l.Sold }
func (l Lot) Cost() int64         { return l.Remaining() * l.UnitCost }
func (l Lot) TargetGross() int64  { return ceil(l.UnitCost*11500, int64(l.Target)) }
func (l Lot) TargetNet() int64    { return ceil(l.UnitCost*10000, int64(l.Target)) }
func (l Lot) EstimatedNet() int64 { return rounded(l.Quote*l.Remaining()*100, 115) }
func (l Lot) DiscountText() string {
	r, e := Discount(l.UnitCost, l.Quote)
	if e != nil {
		return "—"
	}
	return Ratio(r)
}
func (l Lot) Ready() bool {
	return l.Remaining() > 0 && l.Quote > 0 && l.UnitCost*11500 <= l.Quote*int64(l.Target)
}
func (l Lot) Fresh() bool {
	t, e := time.Parse(time.RFC3339, l.QuoteAt)
	return e == nil && !t.After(time.Now()) && time.Since(t) < 24*time.Hour
}
func (l Lot) QuoteTime() string {
	t, e := time.Parse(time.RFC3339, l.QuoteAt)
	if e != nil {
		return "暂无"
	}
	return t.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04") + "（上海时间）"
}
func (l Lot) StatusText() string {
	if l.Remaining() == 0 {
		return "已售完"
	}
	if l.Status == "listed" {
		return "已挂售"
	}
	return "持有中"
}
func validateLot(l Lot) error {
	if strings.TrimSpace(l.Name) == "" || len(l.Name) > 300 || len(l.HashName) > 300 || len(l.Platform) > 80 {
		return fmt.Errorf("请填写物品名称（最多 300 字节）及有效平台名称")
	}
	if l.Quantity < 1 || l.Quantity > MaxQuantity || l.UnitCost < 1 || l.UnitCost > MaxMoney || l.Quote < 0 || l.Quote > MaxMoney {
		return fmt.Errorf("数量或金额超出允许范围")
	}
	if l.Target < 1 || l.Target > 100000 {
		return fmt.Errorf("目标比例应大于 0 且不超过 10，最多四位小数")
	}
	if l.Status != "held" && l.Status != "listed" {
		return fmt.Errorf("持仓状态不正确")
	}
	d, e := time.Parse("2006-01-02", l.BoughtOn)
	if e != nil || d.Format("2006-01-02") != l.BoughtOn {
		return fmt.Errorf("买入日期不正确")
	}
	return nil
}

type Sale struct {
	ID, LotID, Quantity, UnitCost, Received int64
	Name, SoldOn                            string
	Voided                                  bool
}

func (s Sale) Cost() int64 { return s.Quantity * s.UnitCost }
func (s Sale) DiscountText() string {
	if s.Received == 0 {
		return "—"
	}
	r := rounded(s.Cost()*10000, s.Received)
	return fmt.Sprintf("%d.%04d", r/10000, r%10000)
}
