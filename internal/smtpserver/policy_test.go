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

func TestDomainPolicyRejectsAdminThenUserRecipients(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()
	if err := store.SaveAdminMailboxSettings(ctx, models.AppSettings{AcceptedRcptDomains: "admin.test"}); err != nil {
		t.Fatalf("save admin mailbox: %v", err)
	}
	if _, err := store.CreateUser(ctx, "user1", "hash", models.AppSettings{AcceptedRcptDomains: "user.test"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	policy, err := NewDomainPolicy(DomainPolicyConfig{}, store)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	session := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	if response := policy.OnRcptTo(session, "first@admin.test"); response != nil {
		t.Fatalf("admin recipient rejected: %v", response)
	}
	if !session.OwnerSet || session.OwnerUserID != 0 {
		t.Fatalf("admin owner not resolved: %+v", session)
	}
	if response := policy.OnRcptTo(session, "second@user.test"); response == nil || response.Code != 550 {
		t.Fatalf("expected mixed-owner rejection, got %#v", response)
	}
	if session.OwnerUserID != 0 {
		t.Fatalf("owner changed after rejection: %+v", session)
	}
}

func TestDomainPolicyRejectsUserThenAdminRecipients(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()
	if err := store.SaveAdminMailboxSettings(ctx, models.AppSettings{AcceptedRcptDomains: "admin.test"}); err != nil {
		t.Fatalf("save admin mailbox: %v", err)
	}
	user, err := store.CreateUser(ctx, "user1", "hash", models.AppSettings{AcceptedRcptDomains: "user.test"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	policy, err := NewDomainPolicy(DomainPolicyConfig{}, store)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	session := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	if response := policy.OnRcptTo(session, "first@user.test"); response != nil {
		t.Fatalf("user recipient rejected: %v", response)
	}
	if response := policy.OnRcptTo(session, "second@admin.test"); response == nil || response.Code != 550 {
		t.Fatalf("expected mixed-owner rejection, got %#v", response)
	}
	if session.OwnerUserID != user.ID {
		t.Fatalf("owner changed after rejection: %+v", session)
	}
}

func TestDomainPolicyDoesNotRouteUsersWithoutRecipientDomains(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if _, err := store.CreateUser(context.Background(), "user1", "hash", models.AppSettings{}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	policy, err := NewDomainPolicy(DomainPolicyConfig{}, store)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	session := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	response := policy.OnRcptTo(session, "recipient@unassigned.test")
	if response == nil || response.Code != 550 {
		t.Fatalf("expected unrouted recipient rejection, got %#v", response)
	}
	if session.OwnerSet {
		t.Fatalf("unrouted session unexpectedly acquired an owner: %+v", session)
	}
}

func TestDomainPolicyKeepsMailFailRulesIsolatedPerUser(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()
	rule := func(trigger, message string) models.MailFailRule {
		return models.MailFailRule{Name: trigger, Trigger: trigger, Stage: "rcpt", Action: "reject", Code: 550, Message: message}
	}
	user1, err := store.CreateUser(ctx, "user1", "hash", models.AppSettings{
		AcceptedRcptDomains: "alpha.test", MailFailEnabled: true, MailFailRules: []models.MailFailRule{rule("alpha-block", "alpha rule")},
	})
	if err != nil {
		t.Fatalf("create user1: %v", err)
	}
	if _, err := store.CreateUser(ctx, "user2", "hash", models.AppSettings{
		AcceptedRcptDomains: "beta.test", MailFailEnabled: true, MailFailRules: []models.MailFailRule{rule("beta-block", "beta rule")},
	}); err != nil {
		t.Fatalf("create user2: %v", err)
	}
	policy, err := NewDomainPolicy(DomainPolicyConfig{}, store)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	allowed := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	if response := policy.OnRcptTo(allowed, "user+beta-block@alpha.test"); response != nil {
		t.Fatalf("user2 rule leaked into user1 policy: %v", response)
	}
	if allowed.OwnerUserID != user1.ID {
		t.Fatalf("unexpected owner: %+v", allowed)
	}

	blocked := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	response := policy.OnRcptTo(blocked, "user+alpha-block@alpha.test")
	if response == nil || response.Message != "alpha rule" {
		t.Fatalf("expected user1 rule, got %#v", response)
	}
}

func TestDomainPolicyKeepsDataRulesIsolatedPerUser(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()
	rule := func(trigger, message string) models.MailFailRule {
		return models.MailFailRule{Name: trigger, Trigger: trigger, Stage: "data", Action: "reject", Code: 552, Message: message}
	}
	userA, err := store.CreateUser(ctx, "user-a", "hash", models.AppSettings{
		AcceptedRcptDomains: "a.test", MailFailEnabled: true, MailFailRules: []models.MailFailRule{rule("a-quota", "A quota")},
	})
	if err != nil {
		t.Fatalf("create user A: %v", err)
	}
	if _, err := store.CreateUser(ctx, "user-b", "hash", models.AppSettings{
		AcceptedRcptDomains: "b.test", MailFailEnabled: true, MailFailRules: []models.MailFailRule{rule("b-quota", "B quota")},
	}); err != nil {
		t.Fatalf("create user B: %v", err)
	}
	policy, err := NewDomainPolicy(DomainPolicyConfig{}, store)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	allowedRecipient := "user+b-quota@a.test"
	allowed := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	if response := policy.OnRcptTo(allowed, allowedRecipient); response != nil {
		t.Fatalf("RCPT failed: %v", response)
	}
	allowed.RcptTo = append(allowed.RcptTo, allowedRecipient)
	if response := policy.OnData(allowed); response != nil {
		t.Fatalf("user B DATA rule leaked into user A: %v", response)
	}
	if allowed.OwnerUserID != userA.ID {
		t.Fatalf("unexpected owner: %+v", allowed)
	}

	blockedRecipient := "user+a-quota@a.test"
	blocked := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	if response := policy.OnRcptTo(blocked, blockedRecipient); response != nil {
		t.Fatalf("RCPT failed: %v", response)
	}
	blocked.RcptTo = append(blocked.RcptTo, blockedRecipient)
	response := policy.OnData(blocked)
	if response == nil || response.Code != 552 || response.Message != "A quota" {
		t.Fatalf("expected user A DATA rule, got %#v", response)
	}
}

func TestDomainPolicyRejectsRuntimeAmbiguityBetweenRegexPolicies(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()
	if _, err := store.CreateUser(ctx, "user1", "hash", models.AppSettings{AcceptedRcptDomains: `.*@shared\.test$`}); err != nil {
		t.Fatalf("create user1: %v", err)
	}
	if _, err := store.CreateUser(ctx, "user2", "hash", models.AppSettings{AcceptedRcptDomains: `^inbox@.*\.test$`}); err != nil {
		t.Fatalf("create user2: %v", err)
	}
	policy, err := NewDomainPolicy(DomainPolicyConfig{}, store)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	session := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	response := policy.OnRcptTo(session, "inbox@shared.test")
	if response == nil || response.Code != 550 || !strings.Contains(strings.ToLower(response.Message), "ambiguous") {
		t.Fatalf("expected safe ambiguity rejection, got %#v", response)
	}
	if session.OwnerSet {
		t.Fatalf("ambiguous recipient acquired an owner: %+v", session)
	}
}

func TestDomainPolicyRejectsCatchAllUserWhenAnotherMailboxHasRecipientDomains(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	if err := store.SaveAdminMailboxSettings(ctx, models.AppSettings{
		AcceptedRcptDomains: "mp.quiering.com",
	}); err != nil {
		t.Fatalf("save admin mailbox settings: %v", err)
	}
	if _, err := store.CreateUser(ctx, "ricardo", "hash", models.AppSettings{}); err != nil {
		t.Fatalf("create unrestricted user: %v", err)
	}

	policy, err := NewDomainPolicy(DomainPolicyConfig{}, store)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	session := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}

	response := policy.OnRcptTo(session, "user@hotmail.com")
	if response == nil {
		t.Fatal("expected recipient rejection")
	}
	if response.Code != 550 {
		t.Fatalf("expected 550 rejection, got %d", response.Code)
	}
	if !strings.Contains(strings.ToLower(response.Message), "recipient not allowed") {
		t.Fatalf("unexpected response message: %q", response.Message)
	}

	adminSession := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	if response := policy.OnRcptTo(adminSession, "user@mp.quiering.com"); response != nil {
		t.Fatalf("unexpected rcpt response for admin mailbox: %v", response)
	}
	if adminSession.OwnerUserID != 0 {
		t.Fatalf("expected admin mailbox owner id 0, got %d", adminSession.OwnerUserID)
	}
}

func TestDomainPolicyAppliesAdminMailboxDataRule(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()
	if err := store.SaveAdminMailboxSettings(ctx, models.AppSettings{
		AcceptedRcptDomains: "mt.quiering.com",
		MailFailEnabled:     true,
		MailFailRules: []models.MailFailRule{{
			Name:         "quota",
			Trigger:      "mf-quota",
			Stage:        "data",
			Action:       "reject",
			Code:         552,
			EnhancedCode: "5.2.2",
			Message:      "Mailbox full",
		}},
	}); err != nil {
		t.Fatalf("save admin mailbox settings: %v", err)
	}

	policy, err := NewDomainPolicy(DomainPolicyConfig{}, store)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	session := &SessionMetadata{RemoteIP: "127.0.0.1", MailFrom: "sender@outside.test"}
	recipient := "2026082101+mf-quota@mt.quiering.com"
	if response := policy.OnRcptTo(session, recipient); response != nil {
		t.Fatalf("unexpected rcpt response: %v", response)
	}
	session.RcptTo = append(session.RcptTo, recipient)

	response := policy.OnData(session)
	if response == nil {
		t.Fatal("expected admin mailbox DATA rejection")
	}
	if response.Code != 552 || response.Message != "5.2.2 Mailbox full" {
		t.Fatalf("unexpected DATA response: %#v", response)
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
