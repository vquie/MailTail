package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleAppConfinesStaticFiles(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	staticDir := filepath.Join(parent, "web")
	if err := os.MkdirAll(filepath.Join(staticDir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"index.html": "<html>app</html>", "assets/app.js": "console.log('app');"} {
		if err := os.WriteFile(filepath.Join(staticDir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside-test-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"escape.txt": "../outside.txt", "absolute.txt": outside, "escape-dir": "..", "internal.js": "assets/app.js"} {
		if err := os.Symlink(target, filepath.Join(staticDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{staticDir: staticDir}
	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{
		{"/", 200, "<html>app</html>"}, {"/assets/app.js", 200, "console.log('app');"},
		{"/internal.js", 200, "console.log('app');"}, {"/inbox", 200, "<html>app</html>"},
		{"/inbox/", 200, "<html>app</html>"}, {"/assets/", 200, "<html>app</html>"},
		{"/missing.js", 200, "<html>app</html>"}, {"/api/missing", 404, ""},
		{"/escape.txt", 404, ""}, {"/absolute.txt", 404, ""}, {"/escape-dir/outside.txt", 404, ""},
		{"/../outside.txt", 404, ""}, {"/%2e%2e/outside.txt", 404, ""},
		{"/assets/../../outside.txt", 404, ""}, {"/assets/%2e%2e/%2e%2e/outside.txt", 404, ""},
		{"/index.html", 301, ""}, {"/assets/app.js/", 200, "console.log('app');"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			server.handleApp(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.status {
				t.Fatalf("status=%d, want %d", rec.Code, tc.status)
			}
			if tc.body != "" && rec.Body.String() != tc.body {
				t.Fatalf("unexpected response: %q", rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "outside-test-marker") {
				t.Fatal("served a file outside the static root")
			}
		})
	}
	t.Run("index redirect preserves query", func(t *testing.T) {
		rec := httptest.NewRecorder()
		server.handleApp(rec, httptest.NewRequest(http.MethodGet, "/index.html?view=inbox", nil))
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "./?view=inbox" {
			t.Fatal("index redirect changed or lost query parameters")
		}
	})
	t.Run("head", func(t *testing.T) {
		rec := httptest.NewRecorder()
		server.handleApp(rec, httptest.NewRequest(http.MethodHead, "/assets/app.js", nil))
		if rec.Code != 200 || rec.Body.Len() != 0 {
			t.Fatal("HEAD response changed")
		}
	})
	t.Run("range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
		req.Header.Set("Range", "bytes=0-6")
		rec := httptest.NewRecorder()
		server.handleApp(rec, req)
		if rec.Code != 206 || rec.Body.String() != "console" {
			t.Fatal("range response changed")
		}
	})
}

func TestHandleAppRejectsEscapingIndexSymlink(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	staticDir := filepath.Join(parent, "web")
	if err := os.Mkdir(staticDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "outside.html"), []byte("outside-index-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside.html", filepath.Join(staticDir, "index.html")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/inbox", "/index.html"} {
		rec := httptest.NewRecorder()
		(&Server{staticDir: staticDir}).handleApp(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code < 400 || strings.Contains(rec.Body.String(), "outside-index-marker") {
			t.Fatalf("escaping index was not rejected for %s: status=%d", path, rec.Code)
		}
	}
}

func TestHandleAppMissingAssets(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		rec := httptest.NewRecorder()
		(&Server{staticDir: dir}).handleApp(rec, httptest.NewRequest(http.MethodGet, "/inbox", nil))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "MailTail web assets not found") {
			t.Fatal("missing assets response changed")
		}
	}
}
