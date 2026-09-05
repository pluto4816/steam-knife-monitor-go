package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type Sender interface {
	Send(ctx context.Context, alert store.Alert) error
}

type SendResult struct {
	PendingCount int
	SentCount    int
}

const noSuitableItemsMessage = "没有合适物品"

func SendC5PurchaseReceipt(
	ctx context.Context,
	sender Sender,
	receipt C5PurchaseReceipt,
) error {
	if sender == nil {
		return fmt.Errorf("sender cannot be nil")
	}
	if receipt.ManualTest {
		message := fmt.Sprintf(
			"人工测试购买成功\n名称：%s\n购买价格：%s",
			receipt.NameCN,
			receipt.PurchasePrice,
		)
		if err := sender.Send(ctx, store.Alert{Message: message}); err != nil {
			return fmt.Errorf("send manual C5 purchase test receipt: %w", err)
		}
		return nil
	}
	message := fmt.Sprintf(
		"名称：%s\n购买价格：%s\nsteam价格：%s\n折扣：%s",
		receipt.NameCN,
		receipt.PurchasePrice,
		receipt.SteamPrice,
		formatRatio(receipt.Ratio),
	)
	if err := sender.Send(ctx, store.Alert{Message: message}); err != nil {
		return fmt.Errorf("send C5 purchase receipt: %w", err)
	}
	return nil
}

func SendPendingAlerts(
	ctx context.Context,
	stateStore *store.Store,
	sender Sender,
) (SendResult, error) {
	if sender == nil {
		return SendResult{}, fmt.Errorf("sender cannot be nil")
	}
	alerts, err := stateStore.ListPendingAlerts(ctx)
	if err != nil {
		return SendResult{}, fmt.Errorf("list pending alerts: %w", err)
	}
	result := SendResult{PendingCount: len(alerts)}
	for _, alert := range alerts {
		if err := sender.Send(ctx, alert); err != nil {
			return result, fmt.Errorf("send alert %d: %w", alert.ID, err)
		}
		if err := stateStore.MarkAlertSent(ctx, alert.ID, time.Now().UTC()); err != nil {
			return result, fmt.Errorf("mark alert %d sent: %w", alert.ID, err)
		}
		result.SentCount++
	}
	return result, nil
}

func SendNoSuitableItemsIfNeeded(
	ctx context.Context,
	sender Sender,
	tierCounts store.ItemTierCounts,
) (bool, error) {
	if tierCounts.A > 0 {
		return false, nil
	}
	if sender == nil {
		return false, fmt.Errorf("sender cannot be nil")
	}
	if err := sender.Send(ctx, store.Alert{Message: noSuitableItemsMessage}); err != nil {
		return false, fmt.Errorf("send no-suitable-items notification: %w", err)
	}
	return true, nil
}

func newAlertDraft(
	item collector.MarketItem,
	kind core.AlertKind,
	aEntryAlertID *int64,
	createdAt time.Time,
) store.AlertDraft {
	return store.AlertDraft{
		ItemID:        item.ItemID,
		Kind:          kind,
		AEntryAlertID: aEntryAlertID,
		Ratio:         item.MarketData.Ratio,
		Message:       alertMessage(item),
		CreatedAt:     createdAt,
	}
}

func alertMessage(item collector.MarketItem) string {
	return fmt.Sprintf(
		"名称：%s\n折扣：%s\nsteam成交量：%d\nsteam价格：%s\n平台：%s\n平台价格：%s",
		item.NameCN,
		formatRatio(item.MarketData.Ratio),
		item.MarketData.SteamDailyVolume,
		item.SteamPrice,
		item.LowestPlatform,
		item.LowestPlatformPrice,
	)
}

func formatRatio(ratio core.RatioBP) string {
	value := int64(ratio)
	formatted := fmt.Sprintf("%d.%04d", value/10000, value%10000)
	return strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
}
