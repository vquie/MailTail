package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const sessionCookieName = "mailtail_session"

type AuthConfig struct {
	Username string
	Password string
	Realm    string
}

type SessionAuth struct {
	config AuthConfig
}

func NewSessionAuth(config AuthConfig) *SessionAuth {
	return &SessionAuth{config: config}
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
		case r.URL.Path == "/auth/logout":
			a.handleLogout(w, r)
			return
		}

		if a.isAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

func (a *SessionAuth) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.serveLoginPage(w, "Invalid login request.")
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if !a.credentialsMatch(username, password) {
		a.serveLoginPage(w, "Invalid username or password.")
		return
	}

	value, expiresAt, err := a.createSessionValue()
	if err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *SessionAuth) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *SessionAuth) isAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return a.validateSessionValue(cookie.Value)
}

func (a *SessionAuth) credentialsMatch(username, password string) bool {
	usernameMatch := subtle.ConstantTimeCompare([]byte(a.config.Username), []byte(username)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(a.config.Password), []byte(password)) == 1
	return usernameMatch && passwordMatch
}

func (a *SessionAuth) createSessionValue() (string, time.Time, error) {
	expiresAt := time.Now().Add(24 * time.Hour).UTC()
	payload := fmt.Sprintf("%s|%d", a.config.Username, expiresAt.Unix())
	mac := hmac.New(sha256.New, []byte(a.config.Password))
	if _, err := mac.Write([]byte(payload)); err != nil {
		return "", time.Time{}, err
	}
	signature := hex.EncodeToString(mac.Sum(nil))
	token := payload + "|" + signature
	return base64.RawURLEncoding.EncodeToString([]byte(token)), expiresAt, nil
}

func (a *SessionAuth) validateSessionValue(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return false
	}

	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return false
	}

	username, expiresRaw, signature := parts[0], parts[1], parts[2]
	if subtle.ConstantTimeCompare([]byte(username), []byte(a.config.Username)) != 1 {
		return false
	}

	expiresUnix, err := strconv.ParseInt(expiresRaw, 10, 64)
	if err != nil || time.Now().UTC().After(time.Unix(expiresUnix, 0).UTC()) {
		return false
	}

	payload := username + "|" + expiresRaw
	mac := hmac.New(sha256.New, []byte(a.config.Password))
	if _, err := mac.Write([]byte(payload)); err != nil {
		return false
	}
	expectedSignature := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(signature), []byte(expectedSignature)) == 1
}

func (a *SessionAuth) serveLoginPage(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
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
