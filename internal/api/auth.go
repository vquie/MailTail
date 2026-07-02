package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/storage"
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
	config AuthConfig
	store  storage.Store
}

func NewSessionAuth(config AuthConfig, store storage.Store) *SessionAuth {
	return &SessionAuth{config: config, store: store}
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

		record, err := a.currentSession(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load session")
			return
		}
		if record == nil {
			a.rejectUnauthorized(w, r)
			return
		}

		if requiresCSRFFProtection(r.Method) && !a.validCSRF(r, *record) {
			writeError(w, http.StatusForbidden, "invalid csrf token")
			return
		}

		principal := models.SessionPrincipal{
			Username: record.Username,
			IsAdmin:  record.IsAdmin,
			UserID:   record.UserID,
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal)))
	})
}

func (a *SessionAuth) handleLogin(w http.ResponseWriter, r *http.Request) {
	limited, err := a.loginLimited(r)
	if err != nil {
		http.Error(w, "Failed to process login", http.StatusInternalServerError)
		return
	}
	if limited {
		a.serveLoginPageWithStatus(w, "Too many login attempts. Please wait and try again.", http.StatusTooManyRequests)
		return
	}

	if err := r.ParseForm(); err != nil {
		_ = a.recordLoginAttempt(r)
		a.serveLoginPage(w, "Invalid login request.")
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	sessionUsername := username
	userID := int64(0)
	isAdmin := false
	switch {
	case a.adminCredentialsMatch(username, password):
		sessionUsername = a.config.Username
		isAdmin = true
	default:
		credentials, ok, err := a.store.GetUserByUsername(r.Context(), username)
		if err != nil {
			http.Error(w, "Failed to process login", http.StatusInternalServerError)
			return
		}
		if !ok || !verifyPassword(password, credentials.PasswordHash) {
			_ = a.recordLoginAttempt(r)
			a.serveLoginPage(w, "Invalid username or password.")
			return
		}
		sessionUsername = credentials.User.Username
		userID = credentials.User.ID
	}

	if !isAdmin && userID == 0 {
		_ = a.recordLoginAttempt(r)
		a.serveLoginPage(w, "Invalid username or password.")
		return
	}

	if err := a.clearLoginAttempts(r); err != nil {
		http.Error(w, "Failed to process login", http.StatusInternalServerError)
		return
	}

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

	if err := a.store.CreateAuthSession(r.Context(), models.AuthSession{
		SessionID: sessionID,
		Username:  sessionUsername,
		UserID:    userID,
		IsAdmin:   isAdmin,
		CSRFToken: csrfToken,
		ExpiresAt: expiresAt,
	}); err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	setSessionCookies(w, r, sessionID, csrfToken, expiresAt)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *SessionAuth) handleLogout(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := a.sessionIDFromRequest(r)
	if ok {
		_ = a.store.DeleteAuthSession(r.Context(), sessionID)
	}

	clearSessionCookies(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *SessionAuth) currentSession(r *http.Request) (*models.AuthSession, error) {
	sessionID, ok := a.sessionIDFromRequest(r)
	if !ok {
		return nil, nil
	}
	record, exists, err := a.store.GetAuthSession(r.Context(), sessionID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	if !record.ExpiresAt.After(time.Now().UTC()) {
		_ = a.store.DeleteAuthSession(r.Context(), sessionID)
		return nil, nil
	}
	return &record, nil
}

func (a *SessionAuth) sessionIDFromRequest(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}

func (a *SessionAuth) adminCredentialsMatch(username, password string) bool {
	usernameMatch := subtle.ConstantTimeCompare([]byte(a.config.Username), []byte(username)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(a.config.Password), []byte(password)) == 1
	return usernameMatch && passwordMatch
}

func (a *SessionAuth) validCSRF(r *http.Request, record models.AuthSession) bool {
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

func (a *SessionAuth) loginLimited(r *http.Request) (bool, error) {
	key := clientIP(r)
	now := time.Now()
	if err := a.store.DeleteExpiredLoginAttempts(r.Context(), now.Add(-15*time.Minute)); err != nil {
		return false, err
	}
	count, err := a.store.CountLoginAttemptsSince(r.Context(), key, now.Add(-15*time.Minute))
	if err != nil {
		return false, err
	}
	return count >= 5, nil
}

func (a *SessionAuth) recordLoginAttempt(r *http.Request) error {
	key := clientIP(r)
	now := time.Now().UTC()
	if err := a.store.DeleteExpiredLoginAttempts(r.Context(), now.Add(-15*time.Minute)); err != nil {
		return err
	}
	return a.store.RecordLoginAttempt(r.Context(), key, now)
}

func (a *SessionAuth) clearLoginAttempts(r *http.Request) error {
	key := clientIP(r)
	return a.store.ClearLoginAttempts(r.Context(), key)
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
	subtitle := "Sign in to continue"
	if realm := strings.TrimSpace(a.config.Realm); realm != "" && !strings.EqualFold(realm, "MailTail") {
		subtitle = realm
	}
	_ = loginTemplate.Execute(w, map[string]string{
		"Message":  message,
		"Subtitle": subtitle,
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
          radial-gradient(circle at top left, rgba(79, 140, 255, 0.10), transparent 24%),
          linear-gradient(180deg, #0a0f16 0%, #0b1118 48%, #0a1017 100%);
        color: #e6edf7;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        min-height: 100vh;
        padding: 24px;
        display: grid;
        place-items: center;
      }
      .card {
        width: min(420px, 100%);
        padding: 22px;
        border: 1px solid rgba(255,255,255,0.07);
        background: rgba(17,24,34,0.96);
        border-radius: 14px;
      }
      h1 { margin: 0 0 4px; font-size: 1.9rem; letter-spacing: -0.04em; }
      p { margin: 0 0 18px; color: rgba(230,237,247,0.72); }
      label { display: grid; gap: 6px; margin-bottom: 14px; font-size: 0.92rem; }
      input {
        width: 100%;
        border: 1px solid rgba(255,255,255,0.08);
        background: rgba(8,12,19,0.78);
        color: inherit;
        border-radius: 10px;
        padding: 11px 12px;
      }
      button {
        border: 1px solid rgba(79,140,255,0.22);
        background: rgba(79,140,255,0.16);
        color: #c7dbff;
        border-radius: 10px;
        padding: 11px 14px;
        font: inherit;
        font-weight: 700;
        cursor: pointer;
        width: 100%;
      }
      button:hover {
        background: rgba(79,140,255,0.22);
      }
      .error {
        margin-bottom: 14px;
        padding: 10px 12px;
        border-radius: 10px;
        background: rgba(245,72,74,0.12);
        border: 1px solid rgba(245,72,74,0.24);
        color: #ffd6d6;
      }
      @media (width <= 860px) {
        body {
          padding: 16px;
        }
        .card {
          width: 100%;
        }
      }
    </style>
  </head>
  <body>
    <form class="card" method="post" action="/auth/login">
      <h1>MailTail</h1>
      <p>{{.Subtitle}}</p>
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
