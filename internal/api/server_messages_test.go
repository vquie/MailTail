package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/parser"
	"github.com/vquie/MailTail/internal/storage"
)

func TestHandleMessagesSupportsTagFilter(t *testing.T) {
	t.Parallel()

	store := newServerTestStore(t)
	service := NewService(store, parser.NewService(), "test", nil, nil)
	server := &Server{service: service}
	ctx := context.Background()

	if _, err := store.CreateMessage(ctx, models.StoredMessage{
		ReceivedAt: time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC),
		MailFrom:   "sender@example.test",
		RcptTo:     []string{"user+alpha@example.test"},
		HeaderFrom: "sender@example.test",
		HeaderTo:   "user+alpha@example.test",
		Subject:    "alpha",
		MessageID:  "<alpha@example.test>",
		Helo:       "mail.example.test",
		RemoteIP:   "127.0.0.1",
		Size:       128,
		Raw:        "raw",
		TextBody:   "body",
	}); err != nil {
		t.Fatalf("create alpha message: %v", err)
	}

	if _, err := store.CreateMessage(ctx, models.StoredMessage{
		ReceivedAt: time.Date(2026, 7, 2, 11, 0, 0, 0, time.UTC),
		MailFrom:   "sender@example.test",
		RcptTo:     []string{"user+beta@example.test"},
		HeaderFrom: "sender@example.test",
		HeaderTo:   "user+beta@example.test",
		Subject:    "beta",
		MessageID:  "<beta@example.test>",
		Helo:       "mail.example.test",
		RemoteIP:   "127.0.0.1",
		Size:       128,
		Raw:        "raw",
		TextBody:   "body",
	}); err != nil {
		t.Fatalf("create beta message: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/messages?tag=beta&limit=25", nil)
	request = request.WithContext(withPrincipal(request.Context(), models.SessionPrincipal{IsAdmin: true, Username: "admin"}))
	recorder := httptest.NewRecorder()

	server.handleMessages(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", recorder.Code, recorder.Body.String())
	}

	var page models.MessagePage
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(page.Messages) != 1 {
		t.Fatalf("expected 1 filtered message, got %d", len(page.Messages))
	}
	if page.Messages[0].Subject != "beta" {
		t.Fatalf("unexpected filtered message: %+v", page.Messages[0])
	}
	if len(page.Messages[0].Tags) != 1 || page.Messages[0].Tags[0] != "beta" {
		t.Fatalf("unexpected message tags: %+v", page.Messages[0].Tags)
	}
	if len(page.AvailableTags) != 2 || page.AvailableTags[0] != "alpha" || page.AvailableTags[1] != "beta" {
		t.Fatalf("unexpected available tags: %+v", page.AvailableTags)
	}
}

func TestHandleMessagesDeleteRespectsTagFilter(t *testing.T) {
	t.Parallel()

	store := newServerTestStore(t)
	service := NewService(store, parser.NewService(), "test", nil, nil)
	server := &Server{service: service}
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
	create("delete me", "user+alpha@example.test")

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/messages?tag=alpha", nil)
	deleteRequest = deleteRequest.WithContext(withPrincipal(deleteRequest.Context(), models.SessionPrincipal{IsAdmin: true, Username: "admin"}))
	deleteRecorder := httptest.NewRecorder()

	server.handleMessages(deleteRecorder, deleteRequest)

	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("unexpected delete status: got %d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/messages?limit=25", nil)
	listRequest = listRequest.WithContext(withPrincipal(listRequest.Context(), models.SessionPrincipal{IsAdmin: true, Username: "admin"}))
	listRecorder := httptest.NewRecorder()

	server.handleMessages(listRecorder, listRequest)

	if listRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected list status: got %d body=%s", listRecorder.Code, listRecorder.Body.String())
	}

	var page models.MessagePage
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(page.Messages) != 1 || page.Messages[0].Subject != "keep" {
		t.Fatalf("unexpected remaining messages: %+v", page.Messages)
	}
	if len(page.AvailableTags) != 1 || page.AvailableTags[0] != "keep" {
		t.Fatalf("unexpected remaining tags: %+v", page.AvailableTags)
	}
}

func newServerTestStore(t *testing.T) *storage.SQLiteStore {
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
