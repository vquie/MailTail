package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vquie/MailTail/internal/storage"
)

type Server struct {
	httpServer *http.Server
	service    *Service
	logger     *log.Logger
	staticDir  string
}

type CORSConfig struct {
	AllowedOrigins map[string]struct{}
}

func NewServer(addr, staticDir string, service *Service, logger *log.Logger, authConfig AuthConfig, corsConfig CORSConfig) *Server {
	server := &Server{
		service:   service,
		logger:    logger,
		staticDir: staticDir,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/app", server.handleAppInfo)
	mux.HandleFunc("/api/messages", server.handleMessages)
	mux.HandleFunc("/api/messages/", server.handleMessageByID)
	mux.HandleFunc("/api/stats", server.handleStats)
	mux.HandleFunc("/", server.handleApp)
	sessionAuth := NewSessionAuth(authConfig)

	server.httpServer = &http.Server{
		Addr:              addr,
		Handler:           sessionAuth.Middleware(loggingMiddleware(logger, corsMiddleware(mux, corsConfig))),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return server
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		messages, err := s.service.ListMessages(r.Context(), r.URL.Query().Get("q"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
	case http.MethodDelete:
		if err := s.service.DeleteAllMessages(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMessageByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/messages/")
	path = strings.Trim(path, "/")
	if path == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	segments := strings.Split(path, "/")
	messageID, err := strconv.ParseInt(segments[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message id")
		return
	}

	if len(segments) == 1 {
		s.handleMessageResource(w, r, messageID)
		return
	}

	switch segments[1] {
	case "raw":
		s.handleRawMessage(w, r, messageID)
	case "attachments":
		if len(segments) != 3 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		attachmentID, err := strconv.ParseInt(segments[2], 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid attachment id")
			return
		}
		s.handleAttachment(w, r, messageID, attachmentID)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) handleMessageResource(w http.ResponseWriter, r *http.Request, id int64) {
	switch r.Method {
	case http.MethodGet:
		message, err := s.service.GetMessage(r.Context(), id)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "message not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": message})
	case http.MethodDelete:
		if err := s.service.DeleteMessage(r.Context(), id); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "message not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRawMessage(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw, err := s.service.GetRawMessage(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "message/rfc822; charset=utf-8")
	_, _ = w.Write([]byte(raw))
}

func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request, messageID, attachmentID int64) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	attachment, content, err := s.service.GetAttachment(r.Context(), messageID, attachmentID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "attachment not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", attachment.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+attachment.FileName+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	stats, err := s.service.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleAppInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.service.AppInfo())
}

func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	cleanPath := strings.TrimPrefix(filepath.Clean(r.URL.Path), string(filepath.Separator))
	requestedPath := filepath.Join(s.staticDir, cleanPath)
	if info, err := os.Stat(requestedPath); err == nil && !info.IsDir() {
		http.ServeFile(w, r, requestedPath)
		return
	}

	indexPath := filepath.Join(s.staticDir, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		http.ServeFile(w, r, indexPath)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("MailTail web assets not found. Build the frontend in /web first."))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func loggingMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isNoisyPollingRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		logger.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func isNoisyPollingRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	switch {
	case r.URL.Path == "/api/app":
		return true
	case r.URL.Path == "/api/stats":
		return true
	case r.URL.Path == "/api/messages":
		return true
	case strings.HasPrefix(r.URL.Path, "/api/messages/") && !strings.Contains(r.URL.Path, "/attachments/") && !strings.HasSuffix(r.URL.Path, "/raw"):
		return true
	default:
		return false
	}
}

func corsMiddleware(next http.Handler, config CORSConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		allowedOrigin := origin != "" && originAllowed(config.AllowedOrigins, origin)

		if allowedOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			if origin != "" && !allowedOrigin {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originAllowed(allowed map[string]struct{}, origin string) bool {
	if len(allowed) == 0 {
		return false
	}
	_, ok := allowed[origin]
	return ok
}
