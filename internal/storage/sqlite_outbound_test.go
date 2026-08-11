package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/vquie/MailTail/internal/models"
)

func TestSQLiteOutboundQueuePersistsAndReschedulesMessages(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "mailtail.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	if err := store.EnqueueOutboundMessage(ctx, models.OutboundMessage{
		EnvelopeFrom: "",
		Recipient:    "sender@example.test",
		Raw:          "Subject: failure\r\n\r\nbody",
		NextAttempt:  now,
	}); err != nil {
		t.Fatalf("enqueue outbound message: %v", err)
	}

	messages, err := store.ListDueOutboundMessages(ctx, now, 10)
	if err != nil {
		t.Fatalf("list due messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one queued message, got %d", len(messages))
	}
	if messages[0].EnvelopeFrom != "" || messages[0].Recipient != "sender@example.test" {
		t.Fatalf("unexpected queued message: %#v", messages[0])
	}

	retryAt := now.Add(time.Minute)
	if err := store.RescheduleOutboundMessage(ctx, messages[0].ID, 1, retryAt, "temporary error"); err != nil {
		t.Fatalf("reschedule message: %v", err)
	}
	if due, err := store.ListDueOutboundMessages(ctx, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("message should not be due before retry: due=%#v err=%v", due, err)
	}
	if due, err := store.ListDueOutboundMessages(ctx, retryAt, 10); err != nil || len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("message should be due after retry: due=%#v err=%v", due, err)
	}
	if err := store.DeleteOutboundMessage(ctx, messages[0].ID); err != nil {
		t.Fatalf("delete outbound message: %v", err)
	}
	if due, err := store.ListDueOutboundMessages(ctx, retryAt, 10); err != nil || len(due) != 0 {
		t.Fatalf("queue should be empty: due=%#v err=%v", due, err)
	}
}
