package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/vquie/MailTail/internal/models"
)

func TestSQLiteStoreEnforcesUserIsolationForAllMessageResources(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "mailtail.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	userA, err := store.CreateUser(ctx, "user-a", "hash", models.AppSettings{AcceptedRcptDomains: "a.test"})
	if err != nil {
		t.Fatalf("create user A: %v", err)
	}
	userB, err := store.CreateUser(ctx, "user-b", "hash", models.AppSettings{AcceptedRcptDomains: "b.test"})
	if err != nil {
		t.Fatalf("create user B: %v", err)
	}

	create := func(owner int64, subject string) int64 {
		t.Helper()
		id, createErr := store.CreateMessage(ctx, models.StoredMessage{
			OwnerUserID: owner,
			ReceivedAt:  time.Now().UTC(),
			MailFrom:    "sender@example.test",
			RcptTo:      []string{"recipient@example.test"},
			HeaderFrom:  "sender@example.test",
			HeaderTo:    "recipient@example.test",
			Subject:     subject,
			MessageID:   "<" + subject + "@example.test>",
			Helo:        "mail.example.test",
			RemoteIP:    "127.0.0.1",
			Size:        128,
			Raw:         "raw-" + subject,
			TextBody:    "body-" + subject,
			Attachments: []models.StoredAttachment{{FileName: subject + ".txt", ContentType: "text/plain", Size: 7, Content: []byte("private")}},
		})
		if createErr != nil {
			t.Fatalf("create %s message: %v", subject, createErr)
		}
		return id
	}
	messageA := create(userA.ID, "user-a")
	messageB := create(userB.ID, "user-b")
	principalA := models.SessionPrincipal{UserID: userA.ID, Username: userA.Username}

	page, err := store.ListMessages(ctx, models.MessageFilter{OwnerUserID: userA.ID, Limit: 25})
	if err != nil {
		t.Fatalf("list user A messages: %v", err)
	}
	if len(page.Messages) != 1 || page.Messages[0].ID != messageA {
		t.Fatalf("user A saw unexpected messages: %+v", page.Messages)
	}
	if _, err := store.GetMessage(ctx, messageB, principalA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("user A accessed user B message: %v", err)
	}
	if _, err := store.GetRawMessage(ctx, messageB, principalA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("user A accessed user B raw message: %v", err)
	}
	adminMessage, err := store.GetMessage(ctx, messageB, models.SessionPrincipal{IsAdmin: true})
	if err != nil || len(adminMessage.Attachments) != 1 {
		t.Fatalf("load attachment metadata as admin: message=%+v err=%v", adminMessage, err)
	}
	if _, _, err := store.GetAttachment(ctx, messageB, adminMessage.Attachments[0].ID, principalA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("user A accessed user B attachment: %v", err)
	}
	if err := store.DeleteMessage(ctx, messageB, principalA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("user A deleted user B message: %v", err)
	}
	stats, err := store.Stats(ctx, principalA)
	if err != nil {
		t.Fatalf("load user A stats: %v", err)
	}
	if stats.MessageCount != 1 {
		t.Fatalf("user A stats include another owner: %+v", stats)
	}
	if err := store.DeleteAllMessages(ctx, principalA, models.MessageFilter{}); err != nil {
		t.Fatalf("delete user A inbox: %v", err)
	}
	if _, err := store.GetMessage(ctx, messageB, models.SessionPrincipal{IsAdmin: true}); err != nil {
		t.Fatalf("user B message was removed by user A: %v", err)
	}
}
