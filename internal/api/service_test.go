package api

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/storage"
)

func TestCreateUserRejectsRecipientOwnershipConflict(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	service := NewService(store, nil, "test", nil, nil)
	ctx := context.Background()

	user1, err := service.CreateUser(ctx, "user1", "secret", models.AppSettings{
		AcceptedRcptDomains: "example.test",
	}, "")
	if err != nil {
		t.Fatalf("create user1: %v", err)
	}

	_, err = service.CreateUser(ctx, "user2", "secret", models.AppSettings{
		AcceptedRcptDomains: "example.test",
	}, "")
	if err == nil {
		t.Fatal("expected ownership conflict")
	}
	if !strings.Contains(err.Error(), "recipient ownership conflicts") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), user1.Username) || strings.Contains(strings.ToLower(err.Error()), "user2") {
		t.Fatalf("error must not expose usernames: %v", err)
	}
}

func TestUpdateUserRejectsRecipientOwnershipConflict(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	service := NewService(store, nil, "test", nil, nil)
	ctx := context.Background()

	user1, err := service.CreateUser(ctx, "user1", "secret", models.AppSettings{
		AcceptedRcptDomains: "alpha.test",
	}, "")
	if err != nil {
		t.Fatalf("create user1: %v", err)
	}
	user2, err := service.CreateUser(ctx, "user2", "secret", models.AppSettings{
		AcceptedRcptDomains: "beta.test",
	}, "")
	if err != nil {
		t.Fatalf("create user2: %v", err)
	}

	_, err = service.UpdateUser(ctx, user2.ID, "user2", "", models.AppSettings{
		AcceptedRcptDomains: "alpha.test",
	}, "")
	if err != ErrMailboxPolicyConflict {
		t.Fatalf("expected ErrMailboxPolicyConflict, got %v", err)
	}

	updated, ok, err := store.GetUser(ctx, user2.ID)
	if err != nil {
		t.Fatalf("load user2: %v", err)
	}
	if !ok {
		t.Fatal("user2 missing after update failure")
	}
	if updated.Settings.AcceptedRcptDomains != "beta.test" {
		t.Fatalf("user2 settings changed unexpectedly: %+v", updated.Settings)
	}
	if user1.Settings.AcceptedRcptDomains != "alpha.test" {
		t.Fatalf("user1 settings changed unexpectedly: %+v", user1.Settings)
	}
}

func TestUpdateAdminMailboxSettingsRejectsRecipientOwnershipConflict(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	service := NewService(store, nil, "test", nil, nil)
	ctx := context.Background()

	if _, err := service.CreateUser(ctx, "user1", "secret", models.AppSettings{
		AcceptedRcptDomains: "example.test",
	}, ""); err != nil {
		t.Fatalf("create user1: %v", err)
	}

	_, err := service.UpdateAdminMailboxSettings(ctx, models.AppSettings{
		AcceptedRcptDomains: "example.test",
	})
	if err != ErrMailboxPolicyConflict {
		t.Fatalf("expected ErrMailboxPolicyConflict, got %v", err)
	}
}

func TestUpdateAdminMailboxSettingsAllowsUsersWithoutRecipientDomains(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	service := NewService(store, nil, "test", nil, nil)
	ctx := context.Background()

	if _, err := service.CreateUser(ctx, "user1", "secret", models.AppSettings{}, ""); err != nil {
		t.Fatalf("create user1: %v", err)
	}

	saved, err := service.UpdateAdminMailboxSettings(ctx, models.AppSettings{
		AcceptedRcptDomains: "example.test",
	})
	if err != nil {
		t.Fatalf("save admin mailbox settings: %v", err)
	}
	if saved.AcceptedRcptDomains != "example.test" {
		t.Fatalf("unexpected saved settings: %+v", saved)
	}
}

func TestCreateUserWithoutRecipientDomainsAllowedAlongsideScopedAdminMailbox(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	service := NewService(store, nil, "test", nil, nil)
	ctx := context.Background()

	if _, err := service.UpdateAdminMailboxSettings(ctx, models.AppSettings{
		AcceptedRcptDomains: "example.test",
	}); err != nil {
		t.Fatalf("save admin mailbox settings: %v", err)
	}

	user, err := service.CreateUser(ctx, "user1", "secret", models.AppSettings{}, "")
	if err != nil {
		t.Fatalf("create user without recipient domains: %v", err)
	}
	if user.Username != "user1" {
		t.Fatalf("unexpected created user: %+v", user)
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
