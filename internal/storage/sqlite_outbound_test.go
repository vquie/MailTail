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

func TestSQLiteOutboundQueueDefersRecipientDomain(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "mailtail.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	now := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		recipient   string
		nextAttempt time.Time
	}{
		{recipient: "fbl@sender.test", nextAttempt: now},
		{recipient: "other@sender.test", nextAttempt: now},
		{recipient: "later@sender.test", nextAttempt: now.Add(10 * time.Minute)},
		{recipient: "fbl@other.test", nextAttempt: now},
	} {
		if err := store.EnqueueOutboundMessage(ctx, models.OutboundMessage{Recipient: item.recipient, Raw: "Subject: report\r\n\r\nbody", NextAttempt: item.nextAttempt}); err != nil {
			t.Fatalf("enqueue %s: %v", item.recipient, err)
		}
	}
	messages, err := store.ListDueOutboundMessages(ctx, now, 10)
	if err != nil || len(messages) != 3 {
		t.Fatalf("list initial messages: messages=%#v err=%v", messages, err)
	}
	retryAt := now.Add(5 * time.Minute)
	if err := store.DeferOutboundMessagesForDomain(ctx, "SENDER.TEST", messages[0].ID, retryAt, "throttled"); err != nil {
		t.Fatalf("defer domain: %v", err)
	}
	due, err := store.ListDueOutboundMessages(ctx, now, 10)
	if err != nil {
		t.Fatalf("list due messages: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("expected excluded message and other domain to remain due, got %#v", due)
	}
	for _, message := range due {
		if message.Recipient == "other@sender.test" {
			t.Fatalf("same-domain message was not deferred: %#v", due)
		}
	}
	due, err = store.ListDueOutboundMessages(ctx, retryAt, 10)
	if err != nil || len(due) != 3 {
		t.Fatalf("expected original due messages at retry time: messages=%#v err=%v", due, err)
	}
	due, err = store.ListDueOutboundMessages(ctx, now.Add(10*time.Minute), 10)
	if err != nil || len(due) != 4 {
		t.Fatalf("later existing retry must not move earlier: messages=%#v err=%v", due, err)
	}
}
