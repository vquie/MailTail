package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vquie/MailTail/internal/api"
	"github.com/vquie/MailTail/internal/parser"
	"github.com/vquie/MailTail/internal/smtpserver"
	"github.com/vquie/MailTail/internal/storage"
)

var version = "dev"

func main() {
	logger := log.New(os.Stdout, "mailtail ", log.LstdFlags|log.Lmsgprefix)

	dataDir := getEnv("MAILTAIL_DATA_DIR", "data")
	httpAddr := getEnv("MAILTAIL_HTTP_ADDR", ":8080")
	smtpAddr := getEnv("MAILTAIL_SMTP_ADDR", ":8025")
	staticDir := getEnv("MAILTAIL_WEB_DIR", filepath.Join("web", "dist"))
	authUsername := strings.TrimSpace(os.Getenv("MAILTAIL_AUTH_USERNAME"))
	authPassword := os.Getenv("MAILTAIL_AUTH_PASSWORD")
	allowedOrigins := parseCSVEnv("MAILTAIL_ALLOWED_ORIGINS")
	smtpLogVerbose := strings.EqualFold(strings.TrimSpace(os.Getenv("MAILTAIL_SMTP_LOG_VERBOSE")), "true")
	mailFailEnabled := strings.EqualFold(strings.TrimSpace(os.Getenv("MAILTAIL_MAILFAIL_ENABLED")), "true")
	mailFailRulesFile := strings.TrimSpace(os.Getenv("MAILTAIL_MAILFAIL_RULES_FILE"))
	acceptedRcptDomains := parseCSVEnv("MAILTAIL_ACCEPTED_RCPT_DOMAINS")
	acceptedFromDomains := parseCSVEnv("MAILTAIL_ACCEPTED_FROM_DOMAINS")
	allowedRemoteIPs := parseCSVEnv("MAILTAIL_ALLOWED_REMOTE_IPS")

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		logger.Fatalf("create data dir: %v", err)
	}
	logger.Printf("version: %s", version)
	if len(acceptedRcptDomains) == 0 {
		logger.Printf("warning: MAILTAIL_ACCEPTED_RCPT_DOMAINS is empty, accepting RCPT TO for all domains")
	} else {
		logger.Printf("accepted RCPT TO patterns: %s", strings.Join(acceptedRcptDomains, ", "))
	}
	if len(acceptedFromDomains) == 0 {
		logger.Printf("warning: MAILTAIL_ACCEPTED_FROM_DOMAINS is empty, accepting MAIL FROM for all domains")
	} else {
		logger.Printf("accepted MAIL FROM patterns: %s", strings.Join(acceptedFromDomains, ", "))
	}
	if len(allowedRemoteIPs) == 0 {
		logger.Printf("warning: MAILTAIL_ALLOWED_REMOTE_IPS is empty, accepting SMTP connections from all remote IPs")
	} else {
		logger.Printf("accepted SMTP remote IPs: %s", strings.Join(allowedRemoteIPs, ", "))
	}
	switch {
	case authUsername == "" && authPassword == "":
		logger.Printf("warning: HTTP auth is disabled, set MAILTAIL_AUTH_USERNAME and MAILTAIL_AUTH_PASSWORD to protect the web UI and API")
	case authUsername == "" || authPassword == "":
		logger.Fatal("invalid auth config: MAILTAIL_AUTH_USERNAME and MAILTAIL_AUTH_PASSWORD must either both be set or both be empty")
	default:
		logger.Printf("HTTP auth enabled for web UI and API")
	}
	if len(allowedOrigins) == 0 {
		logger.Printf("CORS disabled for cross-origin browsers; web UI and API are same-origin by default")
	} else {
		logger.Printf("allowed CORS origins: %s", strings.Join(allowedOrigins, ", "))
	}
	if smtpLogVerbose {
		logger.Printf("verbose SMTP logging enabled")
	}

	var mailFailEngine *smtpserver.MailFailEngine
	switch {
	case !mailFailEnabled && mailFailRulesFile != "":
		logger.Printf("mailfail disabled; ignoring MAILTAIL_MAILFAIL_RULES_FILE=%s", mailFailRulesFile)
	case mailFailEnabled && mailFailRulesFile == "":
		logger.Fatal("invalid mailfail config: MAILTAIL_MAILFAIL_ENABLED=true requires MAILTAIL_MAILFAIL_RULES_FILE")
	case mailFailEnabled:
		engine, err := smtpserver.LoadMailFailEngine(mailFailRulesFile)
		if err != nil {
			logger.Fatalf("load mailfail rules: %v", err)
		}
		mailFailEngine = engine
		logger.Printf("mailfail enabled with %d rule(s) from %s", engine.RuleCount(), mailFailRulesFile)
	default:
		logger.Printf("mailfail disabled")
	}

	store, err := storage.NewSQLiteStore(filepath.Join(dataDir, "mailtail.db"))
	if err != nil {
		logger.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	parseSvc := parser.NewService()
	apiSvc := api.NewService(store, parseSvc, version)
	httpServer := api.NewServer(httpAddr, staticDir, apiSvc, logger, api.AuthConfig{
		Username: authUsername,
		Password: authPassword,
		Realm:    "MailTail",
	}, api.CORSConfig{
		AllowedOrigins: normalizeExactSet(allowedOrigins),
	})

	smtpPolicy, err := smtpserver.NewDomainPolicy(acceptedRcptDomains, acceptedFromDomains, allowedRemoteIPs, mailFailEngine)
	if err != nil {
		logger.Fatalf("invalid smtp policy config: %v", err)
	}
	smtpServer := smtpserver.NewServer(smtpAddr, store, parseSvc, smtpPolicy, logger, smtpLogVerbose)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)

	go func() {
		logger.Printf("http listening on %s", httpAddr)
		errCh <- httpServer.Start()
	}()

	go func() {
		logger.Printf("smtp listening on %s", smtpAddr)
		errCh <- smtpServer.Start(ctx)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Printf("http shutdown: %v", err)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			logger.Fatalf("server failed: %v", err)
		}
	}
}

func normalizeExactSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}

	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	return set
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func parseCSVEnv(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		values = append(values, part)
	}
	return values
}
