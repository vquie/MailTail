package api

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthCookiesAreAlwaysSecure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, target, forwardedProto, forwarded string }{
		{"plain HTTP", "http://mail.example.test/", "", ""},
		{"HTTPS", "https://mail.example.test/", "", ""},
		{"proxy without headers", "http://mail.example.test/", "", ""},
		{"proxy HTTPS", "http://mail.example.test/", "https", ""},
		{"spoofed downgrade", "http://mail.example.test/", "http", "proto=http"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newServerTestStore(t)
			auth := NewSessionAuth(AuthConfig{Username: "root", Password: "admin-secret"}, store)
			handler := auth.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			form := url.Values{"username": {"root"}, "password": {"admin-secret"}}
			req := httptest.NewRequest(http.MethodPost, tc.target+"auth/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			req.Header.Set("Forwarded", tc.forwarded)
			issued := httptest.NewRecorder()
			handler.ServeHTTP(issued, req)
			if issued.Code != http.StatusSeeOther {
				t.Fatalf("login status=%d", issued.Code)
			}
			assertAuthCookieAttributes(t, issued.Result().Cookies(), false)
			cleared := httptest.NewRecorder()
			logout := httptest.NewRequest(http.MethodPost, tc.target+"auth/logout", nil)
			logout.Header = req.Header.Clone()
			addCookies(logout, issued.Result().Cookies())
			handler.ServeHTTP(cleared, logout)
			assertAuthCookieAttributes(t, cleared.Result().Cookies(), true)
		})
	}
}

func assertAuthCookieAttributes(t *testing.T, cookies []*http.Cookie, deleted bool) {
	t.Helper()
	if len(cookies) != 2 {
		t.Fatalf("cookie count=%d, want 2", len(cookies))
	}
	seen := map[string]bool{}
	for _, cookie := range cookies {
		if cookie.Name != sessionCookieName && cookie.Name != csrfCookieName {
			t.Fatal("unexpected cookie name")
		}
		if seen[cookie.Name] {
			t.Fatal("duplicate cookie name")
		}
		seen[cookie.Name] = true
		if !cookie.Secure || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode || cookie.HttpOnly != (cookie.Name == sessionCookieName) {
			t.Fatalf("cookie attributes changed for %s", cookie.Name)
		}
		if deleted {
			if cookie.Value != "" || cookie.MaxAge != -1 || !cookie.Expires.Before(time.Now()) {
				t.Fatal("cookie deletion attributes changed")
			}
		} else if cookie.Value == "" || cookie.MaxAge <= 0 || !cookie.Expires.After(time.Now()) {
			t.Fatal("cookie lifetime changed")
		}
	}
}

func TestSecureCookiesSupportProxyLoginAndLogout(t *testing.T) {
	t.Parallel()
	store := newServerTestStore(t)
	auth := NewSessionAuth(AuthConfig{Username: "root", Password: "admin-secret"}, store)
	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	form := url.Values{"username": {"root"}, "password": {"admin-secret"}}
	// The proxy forwards HTTP without scheme headers; the browser sees HTTPS.
	login := httptest.NewRequest(http.MethodPost, "http://mail.example.test/auth/login", strings.NewReader(form.Encode()))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, login)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status=%d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	assertAuthCookieAttributes(t, cookies, false)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	httpsURL, _ := url.Parse("https://mail.example.test/")
	httpURL, _ := url.Parse("http://mail.example.test/")
	jar.SetCookies(httpsURL, cookies)
	if len(jar.Cookies(httpURL)) != 0 {
		t.Fatal("authentication cookies were sent over HTTP")
	}
	browserCookies := jar.Cookies(httpsURL)
	if len(browserCookies) != 2 {
		t.Fatal("HTTPS cookies were not retained")
	}
	sessionID, csrfToken := "", ""
	for _, cookie := range cookies {
		if cookie.Name == sessionCookieName {
			sessionID = cookie.Value
		}
		if cookie.Name == csrfCookieName {
			csrfToken = cookie.Value
		}
	}
	mutation := httptest.NewRequest(http.MethodPut, "http://mail.example.test/api/settings", nil)
	addCookies(mutation, browserCookies)
	mutation.Header.Set("X-CSRF-Token", csrfToken)
	mutationRec := httptest.NewRecorder()
	handler.ServeHTTP(mutationRec, mutation)
	if mutationRec.Code != http.StatusNoContent {
		t.Fatalf("authenticated CSRF request failed: status=%d", mutationRec.Code)
	}
	logout := httptest.NewRequest(http.MethodPost, "http://mail.example.test/auth/logout", nil)
	addCookies(logout, browserCookies)
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logout)
	if logoutRec.Code != http.StatusSeeOther {
		t.Fatalf("logout status=%d", logoutRec.Code)
	}
	assertAuthCookieAttributes(t, logoutRec.Result().Cookies(), true)
	jar.SetCookies(httpsURL, logoutRec.Result().Cookies())
	if len(jar.Cookies(httpsURL)) != 0 {
		t.Fatal("logout did not remove cookies")
	}
	if _, ok, err := store.GetAuthSession(t.Context(), sessionID); err != nil || ok {
		t.Fatal("logout did not delete the server session")
	}
	unauthorized := httptest.NewRequest(http.MethodGet, "http://mail.example.test/api/settings", nil)
	unauthorizedRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedRec, unauthorized)
	if unauthorizedRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorizedRec.Code)
	}
	assertAuthCookieAttributes(t, unauthorizedRec.Result().Cookies(), true)
}
