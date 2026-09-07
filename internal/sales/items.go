package sales

import (
	"fmt"
	"strconv"
	"strings"
)

// Omit only the catalog's default wear from presentation, never from stored identity.
func shortName(name string) string {
	for _, suffix := range []string{"（久经沙场）", "(久经沙场)", "(Field-Tested)"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSpace(strings.TrimSuffix(name, suffix))
		}
	}
	return name
}

func (w *Web) resolveItem(name, hash, selected string) (string, string, error) {
	if selected != "" {
		id, err := strconv.ParseInt(selected, 10, 64)
		if err == nil && id > 0 {
			for _, item := range w.choices {
				if item.ItemID == id && (name == item.NameCN || name == shortName(item.NameCN)) {
					return item.NameCN, item.MarketHashName, nil
				}
			}
		}
		return "", "", fmt.Errorf("所选物品已失效，请重新搜索并选择")
	}
	var matches int
	fullName, fullHash := name, hash
	for _, item := range w.choices {
		if name == item.NameCN || name == shortName(item.NameCN) {
			matches++
			fullName, fullHash = item.NameCN, item.MarketHashName
		}
	}
	if matches > 1 {
		return "", "", fmt.Errorf("名称对应多个物品，请从搜索结果中选择")
	}
	if matches == 0 && len([]rune(name)) >= 2 {
		for _, item := range w.choices {
			text := strings.ToLower(item.NameCN + " " + item.MarketHashName)
			matched := true
			for _, term := range strings.Fields(strings.ToLower(name)) {
				matched = matched && strings.Contains(text, term)
			}
			if matched {
				return "", "", fmt.Errorf("请点击搜索结果确认具体物品")
			}
		}
	}
	return fullName, fullHash, nil
}
