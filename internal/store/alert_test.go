package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/core"
)

func TestAlertLifecycleAndContext(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	createdAt := time.Date(2026, 8, 19, 15, 0, 0, 123000000, time.UTC)
	state := testItemState(53, createdAt.Add(5*time.Minute))
	draft := AlertDraft{ItemID: state.ItemID, Kind: core.AlertKindAEntry, Ratio: state.Ratio, Message: "entry message", CreatedAt: createdAt}
	if err := stateStore.SaveItemStateAndAlert(ctx, state, &draft); err != nil {
		t.Fatal(err)
	}

	pending, err := stateStore.ListPendingAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending count = %d, want 1", len(pending))
	}
	alert := pending[0]
	if alert.ItemID != state.ItemID || alert.Kind != core.AlertKindAEntry || alert.Ratio != state.Ratio || alert.Message != draft.Message {
		t.Fatalf("pending alert = %#v", alert)
	}
	if alert.Status != AlertStatusPending || alert.SentAt != nil || alert.AEntryAlertID != nil {
		t.Fatalf("pending alert status fields = %#v", alert)
	}
	if !alert.CreatedAt.Equal(createdAt) {
		t.Fatalf("created at = %v, want %v", alert.CreatedAt, createdAt)
	}

	alertContext, err := stateStore.GetAlertContext(ctx, state.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if !alertContext.HasEntry || alertContext.AEntryAlertID != alert.ID || !alertContext.HasPending || alertContext.HasLastSent {
		t.Fatalf("pending context = %#v", alertContext)
	}

	sentAt := createdAt.Add(time.Minute)
	if err := stateStore.MarkAlertSent(ctx, alert.ID, sentAt); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkAlertSent(ctx, alert.ID, sentAt); err == nil {
		t.Fatal("marking an already sent alert succeeded")
	}
	pending, err = stateStore.ListPendingAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending count after send = %d, want 0", len(pending))
	}
	alertContext, err = stateStore.GetAlertContext(ctx, state.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if alertContext.HasPending || !alertContext.HasLastSent || alertContext.LastSentRatio != state.Ratio {
		t.Fatalf("sent context = %#v", alertContext)
	}
}

func TestSaveItemStateAndAlertRollsBackTogether(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	original := testItemState(53, time.Date(2026, 8, 19, 15, 10, 0, 0, time.UTC))
	original.Tier = core.TierB1
	original.Ratio = 6250
	if err := stateStore.SaveItemState(ctx, original); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.db.Exec(`
CREATE TRIGGER fail_alert_insert
BEFORE INSERT ON alerts
BEGIN
    SELECT RAISE(ABORT, 'forced alert failure');
END`); err != nil {
		t.Fatal(err)
	}

	updated := original
	updated.Tier = core.TierA
	updated.Ratio = 5900
	draft := AlertDraft{ItemID: updated.ItemID, Kind: core.AlertKindAEntry, Ratio: updated.Ratio, Message: "entry message", CreatedAt: time.Date(2026, 8, 19, 15, 1, 0, 0, time.UTC)}
	if err := stateStore.SaveItemStateAndAlert(ctx, updated, &draft); err == nil {
		t.Fatal("transaction succeeded despite forced alert failure")
	}

	got, found, err := stateStore.GetItemState(ctx, original.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || got != original {
		t.Fatalf("state after rollback = %#v, found=%t, want %#v", got, found, original)
	}
	pending, err := stateStore.ListPendingAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending alerts after rollback = %d, want 0", len(pending))
	}
}

func TestAlertPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerts.db")
	ctx := context.Background()
	createdAt := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	state := testItemState(53, createdAt.Add(5*time.Minute))
	draft := AlertDraft{ItemID: state.ItemID, Kind: core.AlertKindAEntry, Ratio: state.Ratio, Message: "durable entry", CreatedAt: createdAt}

	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SaveItemStateAndAlert(ctx, state, &draft); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, found, err := second.GetItemState(ctx, state.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || got != state {
		t.Fatalf("reopened state = %#v, found=%t, want %#v", got, found, state)
	}
	pending, err := second.ListPendingAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Message != draft.Message {
		t.Fatalf("reopened pending alerts = %#v", pending)
	}
}

func TestPendingAlertsAreOrderedByID(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	createdAt := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	for _, itemID := range []int64{9, 3} {
		state := testItemState(itemID, createdAt.Add(5*time.Minute))
		draft := AlertDraft{ItemID: itemID, Kind: core.AlertKindAEntry, Ratio: state.Ratio, Message: "message", CreatedAt: createdAt}
		if err := stateStore.SaveItemStateAndAlert(ctx, state, &draft); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := stateStore.ListPendingAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].ID >= pending[1].ID || pending[0].ItemID != 9 || pending[1].ItemID != 3 {
		t.Fatalf("pending order = %#v", pending)
	}
}
