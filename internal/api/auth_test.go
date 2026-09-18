package api

import (
	"crypto/rand"
	"encoding/base64"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/parser"
)

func TestPasswordHashUsesPBKDF2AndVerifiesSafely(t *testing.T) {
	t.Parallel()

	encoded, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !strings.HasPrefix(encoded, passwordHashPrefix+"$") {
		t.Fatalf("unexpected hash format: %q", encoded)
	}
	if !verifyPassword("correct horse battery staple", encoded) {
		t.Fatal("correct password was rejected")
	}
	if verifyPassword("wrong", encoded) {
		t.Fatal("wrong password was accepted")
	}
	if verifyPassword("anything", passwordHashPrefix+"$999999999$bad$bad") {
		t.Fatal("unreasonable iteration count was accepted")
	}
}

func TestLegacyPasswordHashStillVerifiesAndRequestsUpgrade(t *testing.T) {
	t.Parallel()

	salt := make([]byte, passwordSaltSize)
	if _, err := rand.Read(salt); err != nil {
		t.Fatalf("create salt: %v", err)
	}
	sum := deriveLegacyPasswordHash([]byte("legacy-secret"), salt, 1000)
	encoded := legacyPasswordHashPrefix + "$1000$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(sum)
	if !verifyPassword("legacy-secret", encoded) {
		t.Fatal("legacy password was rejected")
	}
	if !passwordHashNeedsUpgrade(encoded) {
		t.Fatal("legacy password hash was not marked for upgrade")
	}
}

func TestLegacyPasswordHashIsUpgradedAfterLogin(t *testing.T) {
	t.Parallel()

	store := newServerTestStore(t)
	salt := []byte("0123456789abcdef")
	sum := deriveLegacyPasswordHash([]byte("legacy-secret"), salt, 1000)
	encoded := legacyPasswordHashPrefix + "$1000$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(sum)
	if _, err := store.CreateUser(t.Context(), "legacy-user", encoded, models.AppSettings{AcceptedRcptDomains: "legacy.test"}); err != nil {
		t.Fatalf("create legacy user: %v", err)
	}
	auth := NewSessionAuth(AuthConfig{Username: "root", Password: "admin-secret"}, store)
	loginForTest(t, auth.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})), "legacy-user", "legacy-secret")
	credentials, ok, err := store.GetUserByUsername(t.Context(), "legacy-user")
	if err != nil || !ok {
		t.Fatalf("load upgraded user: ok=%v err=%v", ok, err)
	}
	if passwordHashNeedsUpgrade(credentials.PasswordHash) {
		t.Fatalf("legacy hash was not upgraded: %q", credentials.PasswordHash)
	}
}

func TestClientIPIgnoresUntrustedForwardingHeaders(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	request.RemoteAddr = "192.0.2.10:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.77")
	request.Header.Set("X-Real-IP", "203.0.113.88")
	if got := clientIP(request); got != "192.0.2.10" {
		t.Fatalf("trusted spoofable forwarding header, got %q", got)
	}
}

func TestAuthenticatedUserIsBlockedFromAdminAPIAndCSRFIsRequired(t *testing.T) {
	t.Parallel()

	store := newServerTestStore(t)
	service := NewService(store, parser.NewService(), "test", nil, nil)
	user, err := service.CreateUser(t.Context(), "alice", "user-secret", models.AppSettings{AcceptedRcptDomains: "alice.test"}, "root")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	server := NewServer(":0", t.TempDir(), service, log.New(io.Discard, "", 0), store, AuthConfig{
		Username: "root", Password: "admin-secret", Realm: "MailTail",
	}, CORSConfig{})

	cookies := loginForTest(t, server.httpServer.Handler, "alice", "user-secret")
	adminRequest := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	addCookies(adminRequest, cookies)
	adminRecorder := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(adminRecorder, adminRequest)
	if adminRecorder.Code != http.StatusForbidden {
		t.Fatalf("user accessed admin API: status=%d body=%s", adminRecorder.Code, adminRecorder.Body.String())
	}

	settingsRequest := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"settings":{}}`))
	settingsRequest.Header.Set("Content-Type", "application/json")
	addCookies(settingsRequest, cookies)
	settingsRecorder := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(settingsRecorder, settingsRequest)
	if settingsRecorder.Code != http.StatusForbidden {
		t.Fatalf("mutation without CSRF was accepted: status=%d body=%s", settingsRecorder.Code, settingsRecorder.Body.String())
	}

	csrfToken := ""
	for _, cookie := range cookies {
		if cookie.Name == csrfCookieName {
			csrfToken = cookie.Value
		}
	}
	if csrfToken == "" {
		t.Fatal("login did not issue a CSRF token")
	}
	validSettingsRequest := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"settings":{"acceptedRcptDomains":"alice.test"}}`))
	validSettingsRequest.Header.Set("Content-Type", "application/json")
	validSettingsRequest.Header.Set("X-CSRF-Token", csrfToken)
	addCookies(validSettingsRequest, cookies)
	validSettingsRecorder := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(validSettingsRecorder, validSettingsRequest)
	if validSettingsRecorder.Code != http.StatusOK {
		t.Fatalf("valid CSRF mutation failed: status=%d body=%s", validSettingsRecorder.Code, validSettingsRecorder.Body.String())
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	addCookies(sessionRequest, cookies)
	sessionRecorder := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(sessionRecorder, sessionRequest)
	if sessionRecorder.Code != http.StatusOK {
		t.Fatalf("session endpoint failed: status=%d body=%s", sessionRecorder.Code, sessionRecorder.Body.String())
	}
	if !strings.Contains(sessionRecorder.Body.String(), `"username":"alice"`) || !strings.Contains(sessionRecorder.Body.String(), `"userId":`+strconv.FormatInt(user.ID, 10)) {
		t.Fatalf("unexpected user session: %s", sessionRecorder.Body.String())
	}
}

func TestAdminCanAccessAdminAPI(t *testing.T) {
	t.Parallel()

	store := newServerTestStore(t)
	service := NewService(store, parser.NewService(), "test", nil, nil)
	server := NewServer(":0", t.TempDir(), service, log.New(io.Discard, "", 0), store, AuthConfig{
		Username: "root", Password: "admin-secret", Realm: "MailTail",
	}, CORSConfig{})
	cookies := loginForTest(t, server.httpServer.Handler, "root", "admin-secret")
	request := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	addCookies(request, cookies)
	recorder := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin API failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCORSAllowsSettingsMethodsAndSecurityHeadersAreSet(t *testing.T) {
	t.Parallel()

	store := newServerTestStore(t)
	service := NewService(store, parser.NewService(), "test", nil, nil)
	server := NewServer(":0", t.TempDir(), service, log.New(io.Discard, "", 0), store, AuthConfig{}, CORSConfig{
		AllowedOrigins: func() map[string]struct{} { return map[string]struct{}{"https://ui.example": {}} },
	})
	request := httptest.NewRequest(http.MethodOptions, "/api/settings", nil)
	request.Header.Set("Origin", "https://ui.example")
	recorder := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("unexpected preflight status: %d", recorder.Code)
	}
	if methods := recorder.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "PUT") || !strings.Contains(methods, "PATCH") {
		t.Fatalf("settings methods missing from CORS response: %q", methods)
	}
	if recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("security headers missing: %v", recorder.Header())
	}
}

func loginForTest(t *testing.T, handler http.Handler, username, password string) []*http.Cookie {
	t.Helper()
	form := url.Values{"username": {username}, "password": {password}}
	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("login failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	return recorder.Result().Cookies()
}

func addCookies(request *http.Request, cookies []*http.Cookie) {
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
}
