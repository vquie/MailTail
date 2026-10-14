package api

import (
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

// Fixed v1 hash of the synthetic password "legacy-secret" with salt
// "0123456789abcdef" and 1,000 iterations of the retired algorithm.
const legacyPasswordHashFixture = "mailtail-pwhash-v1$1000$MDEyMzQ1Njc4OWFiY2RlZg$P7R/LWn4wVcjVGe97dj0AJe1L3ymYUro42x+kQcGmrQ"

func TestLegacyPasswordHashIsRejected(t *testing.T) {
	t.Parallel()

	for _, password := range []string{"legacy-secret", "wrong", ""} {
		if verifyPassword(password, legacyPasswordHashFixture) {
			t.Fatal("legacy hash was accepted")
		}
	}
	// Changing only the format marker must not make a legacy digest valid.
	relabelled := strings.Replace(legacyPasswordHashFixture, "mailtail-pwhash-v1", passwordHashPrefix, 1)
	if verifyPassword("legacy-secret", relabelled) {
		t.Fatal("relabelled legacy hash was accepted")
	}
}

func TestLegacyPasswordLoginRequiresAdminReset(t *testing.T) {
	t.Parallel()

	store := newServerTestStore(t)
	settings := models.AppSettings{AcceptedRcptDomains: "legacy.test"}
	user, err := store.CreateUser(t.Context(), "legacy-user", legacyPasswordHashFixture, settings)
	if err != nil {
		t.Fatalf("create legacy user: %v", err)
	}
	service := NewService(store, parser.NewService(), "test", nil, nil)
	server := NewServer(":0", t.TempDir(), service, log.New(io.Discard, "", 0), store, AuthConfig{
		Username: "root", Password: "admin-secret",
	}, CORSConfig{})
	handler := server.httpServer.Handler
	form := url.Values{"username": {"legacy-user"}, "password": {"legacy-secret"}}
	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Invalid username or password.") {
		t.Fatalf("legacy login was not rejected: status=%d", recorder.Code)
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatal("rejected legacy login issued cookies")
	}
	credentials, ok, err := store.GetUserByUsername(t.Context(), "legacy-user")
	if err != nil || !ok {
		t.Fatalf("load legacy user: ok=%v err=%v", ok, err)
	}
	if credentials.PasswordHash != legacyPasswordHashFixture {
		t.Fatal("rejected legacy login changed the stored hash")
	}

	adminCookies := loginForTest(t, handler, "root", "admin-secret")
	resetRequest := httptest.NewRequest(http.MethodPut, "/api/admin/users/"+strconv.FormatInt(user.ID, 10),
		strings.NewReader(`{"username":"legacy-user","password":"reset-secret","settings":{"acceptedRcptDomains":"legacy.test"}}`))
	resetRequest.Header.Set("Content-Type", "application/json")
	addCookies(resetRequest, adminCookies)
	for _, cookie := range adminCookies {
		if cookie.Name == csrfCookieName {
			resetRequest.Header.Set("X-CSRF-Token", cookie.Value)
		}
	}
	resetRecorder := httptest.NewRecorder()
	handler.ServeHTTP(resetRecorder, resetRequest)
	if resetRecorder.Code != http.StatusOK {
		t.Fatalf("admin reset failed: status=%d", resetRecorder.Code)
	}
	credentials, ok, err = store.GetUserByUsername(t.Context(), "legacy-user")
	if err != nil || !ok {
		t.Fatalf("load reset user: ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(credentials.PasswordHash, passwordHashPrefix+"$") || !verifyPassword("reset-secret", credentials.PasswordHash) {
		t.Fatal("reset did not store a valid PBKDF2 password")
	}
	if verifyPassword("legacy-secret", credentials.PasswordHash) {
		t.Fatal("old password remained valid after reset")
	}
	if credentials.User.ID != user.ID || credentials.User.Settings.AcceptedRcptDomains != settings.AcceptedRcptDomains {
		t.Fatal("password reset changed account identity or settings")
	}
	loginForTest(t, handler, "legacy-user", "reset-secret")
	afterLogin, ok, err := store.GetUserByUsername(t.Context(), "legacy-user")
	if err != nil || !ok || afterLogin.PasswordHash != credentials.PasswordHash {
		t.Fatal("PBKDF2 login changed the stored hash")
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
