package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

func TestProcessMarketItemCreatesEntryAlert(t *testing.T) {
	stateStore := openTestStore(t)
	item := marketItem(53, 5900, 400)
	item.SteamPrice = "10.00"
	if _, err := ProcessMarketItem(context.Background(), stateStore, item, testCheckedAt); err != nil {
		t.Fatal(err)
	}

	pending := pendingAlerts(t, stateStore)
	if len(pending) != 1 || pending[0].Kind != core.AlertKindAEntry || pending[0].Ratio != 5900 {
		t.Fatalf("pending entry alerts = %#v", pending)
	}
	for _, want := range []string{"A_ENTRY", item.NameCN, item.MarketHashName, "0.5900", "5900 RatioBP", "400", "10.00"} {
		if !strings.Contains(pending[0].Message, want) {
			t.Fatalf("message %q does not contain %q", pending[0].Message, want)
		}
	}
}

func TestProcessMarketItemCreatesEntryFromNonA(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, store.ItemState{ItemID: 53, Ratio: 6250, SteamDailyVolume: 300, Tier: core.TierB1})
	if _, err := ProcessMarketItem(context.Background(), stateStore, marketItem(53, 5900, 400), testCheckedAt); err != nil {
		t.Fatal(err)
	}
	pending := pendingAlerts(t, stateStore)
	if len(pending) != 1 || pending[0].Kind != core.AlertKindAEntry {
		t.Fatalf("pending alerts = %#v, want one A-entry", pending)
	}
}

func TestPendingEntrySuppressesImprovementAndPreservesSnapshot(t *testing.T) {
	stateStore := openTestStore(t)
	entryItem := marketItem(53, 5900, 400)
	entryItem.SteamPrice = "10.00"
	if _, err := ProcessMarketItem(context.Background(), stateStore, entryItem, testCheckedAt); err != nil {
		t.Fatal(err)
	}
	originalMessage := pendingAlerts(t, stateStore)[0].Message

	improved := marketItem(53, 5600, 450)
	improved.NameCN = "更新名称"
	improved.SteamPrice = "9.00"
	if _, err := ProcessMarketItem(context.Background(), stateStore, improved, testCheckedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	pending := pendingAlerts(t, stateStore)
	if len(pending) != 1 || pending[0].Kind != core.AlertKindAEntry {
		t.Fatalf("pending alerts after improvement = %#v", pending)
	}
	if pending[0].Message != originalMessage || strings.Contains(pending[0].Message, "9.00") || strings.Contains(pending[0].Message, "5600") {
		t.Fatalf("entry snapshot changed to %q", pending[0].Message)
	}
}

func TestSentEntryEnablesImprovementAndPendingSuppressesMore(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	if _, err := ProcessMarketItem(ctx, stateStore, marketItem(53, 6000, 400), testCheckedAt); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{}
	if _, err := SendPendingAlerts(ctx, stateStore, sender); err != nil {
		t.Fatal(err)
	}
	entryContext, err := stateStore.GetAlertContext(ctx, 53)
	if err != nil {
		t.Fatal(err)
	}
	if !entryContext.HasLastSent || entryContext.LastSentRatio != 6000 {
		t.Fatalf("entry context = %#v", entryContext)
	}

	if _, err := ProcessMarketItem(ctx, stateStore, marketItem(53, 5801, 400), testCheckedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := len(pendingAlerts(t, stateStore)); got != 0 {
		t.Fatalf("pending alerts below threshold = %d, want 0", got)
	}
	if _, err := ProcessMarketItem(ctx, stateStore, marketItem(53, 5800, 400), testCheckedAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	pending := pendingAlerts(t, stateStore)
	if len(pending) != 1 || pending[0].Kind != core.AlertKindAImprovement || pending[0].AEntryAlertID == nil || *pending[0].AEntryAlertID != entryContext.AEntryAlertID {
		t.Fatalf("improvement alert = %#v", pending)
	}

	if _, err := ProcessMarketItem(ctx, stateStore, marketItem(53, 5500, 400), testCheckedAt.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	pending = pendingAlerts(t, stateStore)
	if len(pending) != 1 || pending[0].Ratio != 5800 {
		t.Fatalf("pending improvement was duplicated or changed: %#v", pending)
	}
}

func TestNewAEpisodeCreatesNewEntry(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	saveState(t, stateStore, store.ItemState{ItemID: 53, Ratio: 6250, SteamDailyVolume: 300, Tier: core.TierB1})
	if _, err := ProcessMarketItem(ctx, stateStore, marketItem(53, 5900, 400), testCheckedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := SendPendingAlerts(ctx, stateStore, &recordingSender{}); err != nil {
		t.Fatal(err)
	}
	firstContext, err := stateStore.GetAlertContext(ctx, 53)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessMarketItem(ctx, stateStore, marketItem(53, 6200, 250), testCheckedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := len(pendingAlerts(t, stateStore)); got != 0 {
		t.Fatalf("A exit created pending alert count %d", got)
	}
	if _, err := ProcessMarketItem(ctx, stateStore, marketItem(53, 6000, 400), testCheckedAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	pending := pendingAlerts(t, stateStore)
	if len(pending) != 1 || pending[0].Kind != core.AlertKindAEntry || pending[0].ID == firstContext.AEntryAlertID {
		t.Fatalf("new episode alert = %#v, first entry ID=%d", pending, firstContext.AEntryAlertID)
	}
}

func TestEntryAlertIsNotDuplicatedAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart-idempotency.db")
	ctx := context.Background()
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saveState(t, first, store.ItemState{ItemID: 53, Ratio: 6250, SteamDailyVolume: 300, Tier: core.TierB1})
	item := marketItem(53, 5900, 400)
	if _, err := ProcessMarketItem(ctx, first, item, testCheckedAt); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := ProcessMarketItem(ctx, second, item, testCheckedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	pending := pendingAlerts(t, second)
	if len(pending) != 1 || pending[0].Kind != core.AlertKindAEntry {
		t.Fatalf("pending alerts after restart = %#v, want one durable entry", pending)
	}
}

func TestSendPendingAlertsFailureKeepsBaselineUnset(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	if _, err := ProcessMarketItem(ctx, stateStore, marketItem(53, 5900, 400), testCheckedAt); err != nil {
		t.Fatal(err)
	}
	sendErr := errors.New("sender unavailable")
	result, err := SendPendingAlerts(ctx, stateStore, &recordingSender{err: sendErr})
	if !errors.Is(err, sendErr) {
		t.Fatalf("SendPendingAlerts() error = %v, want %v", err, sendErr)
	}
	if result.PendingCount != 1 || result.SentCount != 0 {
		t.Fatalf("send result = %#v", result)
	}
	if got := len(pendingAlerts(t, stateStore)); got != 1 {
		t.Fatalf("pending alerts = %d, want 1", got)
	}
	alertContext, err := stateStore.GetAlertContext(ctx, 53)
	if err != nil {
		t.Fatal(err)
	}
	if alertContext.HasLastSent || !alertContext.HasPending {
		t.Fatalf("context after failed send = %#v", alertContext)
	}
}

func TestSendPendingAlertsSuccessEstablishesBaseline(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	if _, err := ProcessMarketItem(ctx, stateStore, marketItem(53, 5900, 400), testCheckedAt); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{}
	result, err := SendPendingAlerts(ctx, stateStore, sender)
	if err != nil {
		t.Fatal(err)
	}
	if result.PendingCount != 1 || result.SentCount != 1 || len(sender.alerts) != 1 {
		t.Fatalf("send result = %#v, sender alerts=%d", result, len(sender.alerts))
	}
	alertContext, err := stateStore.GetAlertContext(ctx, 53)
	if err != nil {
		t.Fatal(err)
	}
	if alertContext.HasPending || !alertContext.HasLastSent || alertContext.LastSentRatio != 5900 {
		t.Fatalf("context after successful send = %#v", alertContext)
	}
}

func TestSendPendingAlertsAtLeastOnceAfterMarkFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "at-least-once.db")
	ctx := context.Background()
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessMarketItem(ctx, first, marketItem(53, 5900, 400), testCheckedAt); err != nil {
		t.Fatal(err)
	}
	firstSender := &closingSender{stateStore: first}
	result, err := SendPendingAlerts(ctx, first, firstSender)
	if err == nil {
		t.Fatal("SendPendingAlerts() succeeded after Store was closed before MarkAlertSent")
	}
	if result.SentCount != 0 || firstSender.calls != 1 {
		t.Fatalf("first attempt result=%#v calls=%d", result, firstSender.calls)
	}

	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if got := len(pendingAlerts(t, second)); got != 1 {
		t.Fatalf("pending after mark failure = %d, want 1", got)
	}
	secondSender := &recordingSender{}
	result, err = SendPendingAlerts(ctx, second, secondSender)
	if err != nil {
		t.Fatal(err)
	}
	if result.SentCount != 1 || len(secondSender.alerts) != 1 {
		t.Fatalf("second attempt result=%#v calls=%d", result, len(secondSender.alerts))
	}
}

type recordingSender struct {
	alerts []store.Alert
	err    error
}

func (sender *recordingSender) Send(_ context.Context, alert store.Alert) error {
	sender.alerts = append(sender.alerts, alert)
	return sender.err
}

type closingSender struct {
	stateStore *store.Store
	calls      int
}

func (sender *closingSender) Send(_ context.Context, _ store.Alert) error {
	sender.calls++
	return sender.stateStore.Close()
}

func pendingAlerts(t *testing.T, stateStore *store.Store) []store.Alert {
	t.Helper()
	alerts, err := stateStore.ListPendingAlerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return alerts
}
