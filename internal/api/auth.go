package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"html/template"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "mailtail_session"
	csrfCookieName    = "mailtail_csrf"
)

type AuthConfig struct {
	Username string
	Password string
	Realm    string
}

type SessionAuth struct {
	config       AuthConfig
	sessions     map[string]sessionRecord
	loginLimiter map[string][]time.Time
	mu           sync.Mutex
}

type sessionRecord struct {
	Username  string
	CSRFToken string
	ExpiresAt time.Time
}

func NewSessionAuth(config AuthConfig) *SessionAuth {
	return &SessionAuth{
		config:       config,
		sessions:     make(map[string]sessionRecord),
		loginLimiter: make(map[string][]time.Time),
	}
}

func (c AuthConfig) Enabled() bool {
	return c.Username != "" || c.Password != ""
}

func (a *SessionAuth) Middleware(next http.Handler) http.Handler {
	if !a.config.Enabled() {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login" && r.Method == http.MethodGet:
			a.serveLoginPage(w, "")
			return
		case r.URL.Path == "/auth/login" && r.Method == http.MethodPost:
			a.handleLogin(w, r)
			return
		case r.URL.Path == "/auth/logout" && r.Method == http.MethodPost:
			a.handleLogout(w, r)
			return
		}

		record, ok := a.currentSession(r)
		if !ok {
			a.rejectUnauthorized(w, r)
			return
		}

		if requiresCSRFFProtection(r.Method) && !a.validCSRF(r, record) {
			writeError(w, http.StatusForbidden, "invalid csrf token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *SessionAuth) handleLogin(w http.ResponseWriter, r *http.Request) {
	if a.loginLimited(r) {
		a.serveLoginPageWithStatus(w, "Too many login attempts. Please wait and try again.", http.StatusTooManyRequests)
		return
	}

	if err := r.ParseForm(); err != nil {
		a.recordLoginAttempt(r)
		a.serveLoginPage(w, "Invalid login request.")
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if !a.credentialsMatch(username, password) {
		a.recordLoginAttempt(r)
		a.serveLoginPage(w, "Invalid username or password.")
		return
	}

	a.clearLoginAttempts(r)

	sessionID, err := randomToken(32)
	if err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}
	csrfToken, err := randomToken(32)
	if err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}
	expiresAt := time.Now().Add(24 * time.Hour).UTC()

	a.mu.Lock()
	a.sessions[sessionID] = sessionRecord{
		Username:  a.config.Username,
		CSRFToken: csrfToken,
		ExpiresAt: expiresAt,
	}
	a.mu.Unlock()

	setSessionCookies(w, r, sessionID, csrfToken, expiresAt)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *SessionAuth) handleLogout(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := a.sessionIDFromRequest(r)
	if ok {
		a.mu.Lock()
		delete(a.sessions, sessionID)
		a.mu.Unlock()
	}

	clearSessionCookies(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *SessionAuth) currentSession(r *http.Request) (sessionRecord, bool) {
	sessionID, ok := a.sessionIDFromRequest(r)
	if !ok {
		return sessionRecord{}, false
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	record, exists := a.sessions[sessionID]
	if !exists {
		return sessionRecord{}, false
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		delete(a.sessions, sessionID)
		return sessionRecord{}, false
	}
	return record, true
}

func (a *SessionAuth) sessionIDFromRequest(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}

func (a *SessionAuth) credentialsMatch(username, password string) bool {
	usernameMatch := subtle.ConstantTimeCompare([]byte(a.config.Username), []byte(username)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(a.config.Password), []byte(password)) == 1
	return usernameMatch && passwordMatch
}

func (a *SessionAuth) validCSRF(r *http.Request, record sessionRecord) bool {
	headerToken := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
	if headerToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(headerToken), []byte(record.CSRFToken)) == 1
}

func (a *SessionAuth) rejectUnauthorized(w http.ResponseWriter, r *http.Request) {
	clearSessionCookies(w, r)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *SessionAuth) loginLimited(r *http.Request) bool {
	key := clientIP(r)
	now := time.Now()

	a.mu.Lock()
	defer a.mu.Unlock()

	attempts := pruneAttempts(a.loginLimiter[key], now)
	a.loginLimiter[key] = attempts
	return len(attempts) >= 5
}

func (a *SessionAuth) recordLoginAttempt(r *http.Request) {
	key := clientIP(r)
	now := time.Now()

	a.mu.Lock()
	defer a.mu.Unlock()

	attempts := pruneAttempts(a.loginLimiter[key], now)
	a.loginLimiter[key] = append(attempts, now)
}

func (a *SessionAuth) clearLoginAttempts(r *http.Request) {
	key := clientIP(r)
	a.mu.Lock()
	delete(a.loginLimiter, key)
	a.mu.Unlock()
}

func pruneAttempts(attempts []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-15 * time.Minute)
	kept := attempts[:0]
	for _, attempt := range attempts {
		if attempt.After(cutoff) {
			kept = append(kept, attempt)
		}
	}
	return kept
}

func setSessionCookies(w http.ResponseWriter, r *http.Request, sessionID, csrfToken string, expiresAt time.Time) {
	secure := requestIsHTTPS(r)
	maxAge := int(time.Until(expiresAt).Seconds())

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  expiresAt,
		MaxAge:   maxAge,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrfToken,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  expiresAt,
		MaxAge:   maxAge,
	})
}

func clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	secure := requestIsHTTPS(r)
	for _, name := range []string{sessionCookieName, csrfCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == sessionCookieName,
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
		})
	}
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}

	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		return true
	}

	for _, part := range strings.Split(r.Header.Get("Forwarded"), ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "proto=") && strings.EqualFold(strings.Trim(part[6:], `"`), "https") {
			return true
		}
	}

	return false
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func requiresCSRFFProtection(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func clientIP(r *http.Request) string {
	if forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		return strings.TrimSpace(parts[0])
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (a *SessionAuth) serveLoginPage(w http.ResponseWriter, message string) {
	a.serveLoginPageWithStatus(w, message, http.StatusOK)
}

func (a *SessionAuth) serveLoginPageWithStatus(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginTemplate.Execute(w, map[string]string{
		"Message": message,
		"Realm":   a.config.Realm,
	})
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>MailTail Login</title>
    <style>
      :root {
        color-scheme: dark;
        font-family: "Space Grotesk", sans-serif;
        background:
          radial-gradient(circle at top left, rgba(242, 154, 74, 0.22), transparent 28%),
          radial-gradient(circle at right, rgba(39, 98, 255, 0.18), transparent 30%),
          linear-gradient(160deg, #11131a 0%, #181d2a 55%, #0d1016 100%);
        color: #f8f3ea;
      }
      * { box-sizing: border-box; }
      body { margin: 0; min-height: 100vh; display: grid; place-items: center; }
      .card {
        width: min(420px, calc(100vw - 32px));
        padding: 28px;
        border: 1px solid rgba(255,255,255,0.08);
        background: rgba(255,255,255,0.05);
        border-radius: 10px;
        box-shadow: 0 20px 60px rgba(0,0,0,0.25);
      }
      h1 { margin: 0 0 8px; font-size: 2rem; }
      p { margin: 0 0 18px; color: rgba(248,243,234,0.75); }
      label { display: grid; gap: 6px; margin-bottom: 14px; font-size: 0.92rem; }
      input {
        border: 1px solid rgba(255,255,255,0.08);
        background: rgba(255,255,255,0.06);
        color: inherit;
        border-radius: 6px;
        padding: 11px 12px;
      }
      button {
        border: 0;
        background: linear-gradient(135deg, #f0b36f, #f26a4a);
        color: #11131a;
        border-radius: 6px;
        padding: 11px 14px;
        font: inherit;
        font-weight: 700;
        cursor: pointer;
        width: 100%;
      }
      .error {
        margin-bottom: 14px;
        padding: 10px 12px;
        border-radius: 6px;
        background: rgba(245,72,74,0.16);
        border: 1px solid rgba(245,72,74,0.35);
        color: #ffd6d6;
      }
    </style>
  </head>
  <body>
    <form class="card" method="post" action="/auth/login">
      <h1>MailTail</h1>
      <p>{{if .Realm}}{{.Realm}}{{else}}Sign in to continue{{end}}</p>
      {{if .Message}}<div class="error">{{.Message}}</div>{{end}}
      <label>
        <span>Username</span>
        <input name="username" autocomplete="username" required />
      </label>
      <label>
        <span>Password</span>
        <input type="password" name="password" autocomplete="current-password" required />
      </label>
      <button type="submit">Sign in</button>
    </form>
  </body>
</html>`))
