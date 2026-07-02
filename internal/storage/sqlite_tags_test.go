package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/vquie/MailTail/internal/models"
)

func TestSQLiteStoreBackfillsTagsForExistingMessages(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "mailtail.db")
	seedLegacyMessage(t, dbPath, legacyMessageSeed{
		receivedAt: time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC),
		rcptTo:     []string{"user+alpha@example.test", "other+BETA@example.test", "plain@example.test"},
		subject:    "legacy message",
	})

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open upgraded store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	page, err := store.ListMessages(context.Background(), models.MessageFilter{IncludeAll: true, Limit: 25})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(page.Messages))
	}
	if diff := compareStringSlices(page.Messages[0].Tags, []string{"alpha", "beta"}); diff != "" {
		t.Fatalf("unexpected message tags: %s", diff)
	}
	if diff := compareStringSlices(page.AvailableTags, []string{"alpha", "beta"}); diff != "" {
		t.Fatalf("unexpected available tags: %s", diff)
	}

	filtered, err := store.ListMessages(context.Background(), models.MessageFilter{IncludeAll: true, Tag: "beta", Limit: 25})
	if err != nil {
		t.Fatalf("list filtered messages: %v", err)
	}
	if len(filtered.Messages) != 1 {
		t.Fatalf("expected 1 filtered message, got %d", len(filtered.Messages))
	}
}

func TestSQLiteStoreRemovesOrphanedTagsAfterDelete(t *testing.T) {
	t.Parallel()

	store := newTagTestStore(t)
	ctx := context.Background()
	messageID, err := store.CreateMessage(ctx, models.StoredMessage{
		ReceivedAt: time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC),
		MailFrom:   "sender@example.test",
		RcptTo:     []string{"user+alpha@example.test"},
		HeaderFrom: "sender@example.test",
		HeaderTo:   "user+alpha@example.test",
		Subject:    "tagged",
		MessageID:  "<alpha@example.test>",
		Helo:       "mail.example.test",
		RemoteIP:   "127.0.0.1",
		Size:       128,
		Raw:        "raw",
		TextBody:   "body",
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}

	page, err := store.ListMessages(ctx, models.MessageFilter{IncludeAll: true, Limit: 25})
	if err != nil {
		t.Fatalf("list messages before delete: %v", err)
	}
	if diff := compareStringSlices(page.AvailableTags, []string{"alpha"}); diff != "" {
		t.Fatalf("unexpected tags before delete: %s", diff)
	}

	if err := store.DeleteMessage(ctx, messageID, models.SessionPrincipal{IsAdmin: true}); err != nil {
		t.Fatalf("delete message: %v", err)
	}

	page, err = store.ListMessages(ctx, models.MessageFilter{IncludeAll: true, Limit: 25})
	if err != nil {
		t.Fatalf("list messages after delete: %v", err)
	}
	if len(page.Messages) != 0 {
		t.Fatalf("expected inbox to be empty, got %d messages", len(page.Messages))
	}
	if len(page.AvailableTags) != 0 {
		t.Fatalf("expected tags to be empty after delete, got %v", page.AvailableTags)
	}
}

func TestSQLiteStoreListsAvailableTagsPerOwnerScope(t *testing.T) {
	t.Parallel()

	store := newTagTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, "user1", "hash", models.AppSettings{})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := store.CreateMessage(ctx, models.StoredMessage{
		OwnerUserID: 0,
		ReceivedAt:  time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC),
		MailFrom:    "sender@example.test",
		RcptTo:      []string{"admin+shared@example.test"},
		HeaderFrom:  "sender@example.test",
		HeaderTo:    "admin+shared@example.test",
		Subject:     "admin",
		MessageID:   "<admin@example.test>",
		Helo:        "mail.example.test",
		RemoteIP:    "127.0.0.1",
		Size:        128,
		Raw:         "raw",
		TextBody:    "body",
	}); err != nil {
		t.Fatalf("create admin message: %v", err)
	}

	if _, err := store.CreateMessage(ctx, models.StoredMessage{
		OwnerUserID: user.ID,
		ReceivedAt:  time.Date(2026, 7, 2, 11, 0, 0, 0, time.UTC),
		MailFrom:    "sender@example.test",
		RcptTo:      []string{"user+private@example.test"},
		HeaderFrom:  "sender@example.test",
		HeaderTo:    "user+private@example.test",
		Subject:     "user",
		MessageID:   "<user@example.test>",
		Helo:        "mail.example.test",
		RemoteIP:    "127.0.0.1",
		Size:        128,
		Raw:         "raw",
		TextBody:    "body",
	}); err != nil {
		t.Fatalf("create user message: %v", err)
	}

	adminPage, err := store.ListMessages(ctx, models.MessageFilter{IncludeAll: true, Limit: 25})
	if err != nil {
		t.Fatalf("list admin messages: %v", err)
	}
	if diff := compareStringSlices(adminPage.AvailableTags, []string{"private", "shared"}); diff != "" {
		t.Fatalf("unexpected admin tags: %s", diff)
	}

	userPage, err := store.ListMessages(ctx, models.MessageFilter{OwnerUserID: user.ID, Limit: 25})
	if err != nil {
		t.Fatalf("list user messages: %v", err)
	}
	if diff := compareStringSlices(userPage.AvailableTags, []string{"private"}); diff != "" {
		t.Fatalf("unexpected user tags: %s", diff)
	}
	if len(userPage.Messages) != 1 || !reflect.DeepEqual(userPage.Messages[0].Tags, []string{"private"}) {
		t.Fatalf("unexpected user messages: %+v", userPage.Messages)
	}
}

func TestSQLiteStoreDeleteAllMessagesRespectsFilters(t *testing.T) {
	t.Parallel()

	store := newTagTestStore(t)
	ctx := context.Background()

	create := func(subject, recipient string) {
		t.Helper()
		if _, err := store.CreateMessage(ctx, models.StoredMessage{
			ReceivedAt: time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC),
			MailFrom:   "sender@example.test",
			RcptTo:     []string{recipient},
			HeaderFrom: "sender@example.test",
			HeaderTo:   recipient,
			Subject:    subject,
			MessageID:  "<" + subject + "@example.test>",
			Helo:       "mail.example.test",
			RemoteIP:   "127.0.0.1",
			Size:       128,
			Raw:        "raw",
			TextBody:   "body",
		}); err != nil {
			t.Fatalf("create message %q: %v", subject, err)
		}
	}

	create("keep", "user+keep@example.test")
	create("delete alpha", "user+alpha@example.test")
	create("delete alpha second", "user+alpha@example.test")

	if err := store.DeleteAllMessages(ctx, models.SessionPrincipal{IsAdmin: true}, models.MessageFilter{IncludeAll: true, Tag: "alpha"}); err != nil {
		t.Fatalf("delete filtered messages: %v", err)
	}

	page, err := store.ListMessages(ctx, models.MessageFilter{IncludeAll: true, Limit: 25})
	if err != nil {
		t.Fatalf("list remaining messages: %v", err)
	}
	if len(page.Messages) != 1 || page.Messages[0].Subject != "keep" {
		t.Fatalf("unexpected remaining messages: %+v", page.Messages)
	}
	if diff := compareStringSlices(page.AvailableTags, []string{"keep"}); diff != "" {
		t.Fatalf("unexpected remaining tags: %s", diff)
	}
}

func newTagTestStore(t *testing.T) *SQLiteStore {
	t.Helper()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "mailtail.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

type legacyMessageSeed struct {
	receivedAt time.Time
	rcptTo     []string
	subject    string
}

func seedLegacyMessage(t *testing.T, path string, seed legacyMessageSeed) {
	t.Helper()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer db.Close()

	schema, err := migrationsFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatalf("read legacy schema: %v", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("apply legacy schema: %v", err)
	}

	rcptJSON, err := json.Marshal(seed.rcptTo)
	if err != nil {
		t.Fatalf("marshal rcpt json: %v", err)
	}
	headersJSON, err := json.Marshal([]models.Header{})
	if err != nil {
		t.Fatalf("marshal headers json: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO messages (
			received_at, mail_from, rcpt_to_json, header_from, header_to, subject,
			message_id, helo, remote_ip, size, raw, text_body, html_body, headers_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		seed.receivedAt.Format(time.RFC3339Nano),
		"sender@example.test",
		string(rcptJSON),
		"sender@example.test",
		seed.rcptTo[0],
		seed.subject,
		"<legacy@example.test>",
		"mail.example.test",
		"127.0.0.1",
		128,
		"raw",
		"body",
		"",
		string(headersJSON),
	); err != nil {
		t.Fatalf("insert legacy message: %v", err)
	}
}

func compareStringSlices(got, want []string) string {
	if reflect.DeepEqual(got, want) {
		return ""
	}
	return "got " + stringsOf(got) + ", want " + stringsOf(want)
}

func stringsOf(values []string) string {
	return fmt.Sprint(values)
}
