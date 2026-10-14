package api

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestLoggingMiddlewareEscapesRequestFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, method, target string }{
		{"normal", "GET", "/assets/app.js?token=do-not-log"},
		{"encoded CRLF", "GET", "/inbox%0d%0aFORGED%20entry"},
		{"encoded LF", "GET", "/inbox%0aFORGED"},
		{"terminal controls", "GET", "/inbox%1b%5b2J%09%00"},
		{"unicode separators", "GET", "/inbox%E2%80%A8FORGED%E2%80%A9entry"},
		{"quotes and backslashes", "GET", "/inbox%22%5c%5cnFORGED"},
		{"invalid UTF8", "GET", "/inbox%ff"},
		{"unicode path", "GET", "/postfach/%C3%BCbersicht"},
		{"method controls", "GET\r\nFORGED", "/inbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tc.target, nil)
			request.Method = tc.method
			originalPath, originalQuery := request.URL.Path, request.URL.RawQuery
			var output bytes.Buffer
			calls := 0
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r != request || r.Method != tc.method || r.URL.Path != originalPath || r.URL.RawQuery != originalQuery {
					t.Fatal("logging changed the request")
				}
				w.WriteHeader(http.StatusAccepted)
			})
			rec := httptest.NewRecorder()
			loggingMiddleware(log.New(&output, "", 0), next).ServeHTTP(rec, request)
			if calls != 1 || rec.Code != http.StatusAccepted {
				t.Fatal("logging changed handler execution")
			}
			entry := output.String()
			if !strings.HasSuffix(entry, "\n") || strings.Count(entry, "\n") != 1 {
				t.Fatalf("log entry spans multiple lines: %q", entry)
			}
			line := strings.TrimSuffix(entry, "\n")
			for _, b := range []byte(line) {
				if b < 32 || b > 126 {
					t.Fatalf("unescaped control or non-ASCII byte in log: %q", entry)
				}
			}
			methodLiteral, err := strconv.QuotedPrefix(line)
			if err != nil {
				t.Fatal("method is not delimited")
			}
			method, err := strconv.Unquote(methodLiteral)
			if err != nil || method != tc.method {
				t.Fatal("method value was lost")
			}
			remainder := strings.TrimPrefix(line, methodLiteral)
			if !strings.HasPrefix(remainder, " ") {
				t.Fatal("log field separator missing")
			}
			path, err := strconv.Unquote(strings.TrimPrefix(remainder, " "))
			if err != nil || path != originalPath {
				t.Fatal("path value was lost or additional log fields were injected")
			}
			if strings.Contains(entry, "do-not-log") {
				t.Fatal("query parameters were logged")
			}
		})
	}
}

func TestLoggingMiddlewarePreservesPollingSuppression(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method, path string
		logged       bool
	}{
		{"GET", "/api/app", false}, {"GET", "/api/settings", false}, {"GET", "/api/stats", false},
		{"GET", "/api/messages", false}, {"GET", "/api/messages/1", false},
		{"GET", "/api/messages/1/raw", true}, {"GET", "/api/messages/1/attachments/1", true},
		{"POST", "/api/messages", true},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var output bytes.Buffer
			calls := 0
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) })
			rec := httptest.NewRecorder()
			loggingMiddleware(log.New(&output, "", 0), next).ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if (output.Len() > 0) != tc.logged || calls != 1 || rec.Code != http.StatusNoContent {
				t.Fatal("polling suppression or handler execution changed")
			}
		})
	}
}
