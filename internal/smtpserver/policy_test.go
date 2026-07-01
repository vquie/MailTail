package smtpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/storage"
)

func TestDomainPolicyAssignsRecipientToMatchingUser(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	user1, err := store.CreateUser(ctx, "user1", "hash", models.AppSettings{
		AcceptedRcptDomains: "alpha.test",
	})
	if err != nil {
		t.Fatalf("create user1: %v", err)
	}
	user2, err := store.CreateUser(ctx, "user2", "hash", models.AppSettings{
		AcceptedRcptDomains: "beta.test",
	})
	if err != nil {
		t.Fatalf("create user2: %v", err)
	}

	policy, err := NewDomainPolicy(DomainPolicyConfig{}, store)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	session1 := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	if response := policy.OnRcptTo(session1, "inbox@alpha.test"); response != nil {
		t.Fatalf("unexpected rcpt response for user1: %v", response)
	}
	if session1.OwnerUserID != user1.ID {
		t.Fatalf("expected user1 owner id %d, got %d", user1.ID, session1.OwnerUserID)
	}

	session2 := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	if response := policy.OnRcptTo(session2, "inbox@beta.test"); response != nil {
		t.Fatalf("unexpected rcpt response for user2: %v", response)
	}
	if session2.OwnerUserID != user2.ID {
		t.Fatalf("expected user2 owner id %d, got %d", user2.ID, session2.OwnerUserID)
	}
}

func TestDomainPolicyRejectsMixedRecipientsAcrossUsersWithoutUsernameLeak(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	user1, err := store.CreateUser(ctx, "user1", "hash", models.AppSettings{
		AcceptedRcptDomains: "alpha.test",
	})
	if err != nil {
		t.Fatalf("create user1: %v", err)
	}
	if _, err := store.CreateUser(ctx, "user2", "hash", models.AppSettings{
		AcceptedRcptDomains: "beta.test",
	}); err != nil {
		t.Fatalf("create user2: %v", err)
	}

	policy, err := NewDomainPolicy(DomainPolicyConfig{}, store)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	session := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	if response := policy.OnRcptTo(session, "first@alpha.test"); response != nil {
		t.Fatalf("unexpected rcpt response for first recipient: %v", response)
	}
	if session.OwnerUserID != user1.ID {
		t.Fatalf("expected user1 owner id %d, got %d", user1.ID, session.OwnerUserID)
	}

	response := policy.OnRcptTo(session, "second@beta.test")
	if response == nil {
		t.Fatal("expected mixed-recipient rejection")
	}
	if !strings.Contains(strings.ToLower(response.Message), "different user") {
		t.Fatalf("unexpected response message: %q", response.Message)
	}
	if strings.Contains(strings.ToLower(response.Message), "user1") || strings.Contains(strings.ToLower(response.Message), "user2") {
		t.Fatalf("response must not expose usernames: %q", response.Message)
	}
}

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "mailtail.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
