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
	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/parser"
	"github.com/vquie/MailTail/internal/runtimeconfig"
	"github.com/vquie/MailTail/internal/smtpserver"
	"github.com/vquie/MailTail/internal/storage"
)

var version = "dev"

func main() {
	logger := log.New(os.Stdout, "mailtail ", log.LstdFlags|log.Lmsgprefix)

	authUsername := strings.TrimSpace(os.Getenv("MAILTAIL_AUTH_USERNAME"))
	authPassword := os.Getenv("MAILTAIL_AUTH_PASSWORD")
	dataDir := getEnv("MAILTAIL_DATA_DIR", "data")
	httpAddr := getEnv("MAILTAIL_HTTP_ADDR", ":8080")
	smtpAddr := getEnv("MAILTAIL_SMTP_ADDR", ":8025")
	staticDir := getEnv("MAILTAIL_WEB_DIR", filepath.Join("web", "dist"))

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		logger.Fatalf("create data dir: %v", err)
	}
	logger.Printf("version: %s", version)
	switch {
	case authUsername == "" && authPassword == "":
		logger.Printf("warning: HTTP auth is disabled, set MAILTAIL_AUTH_USERNAME and MAILTAIL_AUTH_PASSWORD to protect the web UI and API")
	case authUsername == "" || authPassword == "":
		logger.Fatal("invalid auth config: MAILTAIL_AUTH_USERNAME and MAILTAIL_AUTH_PASSWORD must either both be set or both be empty")
	default:
		logger.Printf("HTTP auth enabled for web UI and API")
	}
	store, err := storage.NewSQLiteStore(filepath.Join(dataDir, "mailtail.db"))
	if err != nil {
		logger.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	settings := envAppSettings()
	if savedSettings, ok, err := store.LoadAppSettings(context.Background()); err != nil {
		logger.Fatalf("load app settings: %v", err)
	} else if ok {
		settings = savedSettings
		logger.Printf("loaded app settings from database")
	}
	logSettings(logger, settings)

	parseSvc := parser.NewService()
	runtime := runtimeconfig.New(settings)
	smtpPolicy, err := smtpserver.NewDomainPolicy(smtpserver.DomainPolicyConfigFromSettings(settings), store)
	if err != nil {
		logger.Fatalf("invalid smtp policy config: %v", err)
	}
	apiSvc := api.NewService(store, parseSvc, version, runtime, smtpPolicy)
	httpServer := api.NewServer(httpAddr, staticDir, apiSvc, logger, store, api.AuthConfig{
		Username: authUsername,
		Password: authPassword,
		Realm:    "MailTail",
	}, api.CORSConfig{
		AllowedOrigins: runtime.AllowedOrigins,
	})
	smtpServer := smtpserver.NewServer(smtpAddr, store, parseSvc, smtpPolicy, logger, runtime.SMTPLogVerbose)

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

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envAppSettings() models.AppSettings {
	return models.AppSettings{
		AllowedOrigins:      strings.TrimSpace(os.Getenv("MAILTAIL_ALLOWED_ORIGINS")),
		SMTPLogVerbose:      strings.EqualFold(strings.TrimSpace(os.Getenv("MAILTAIL_SMTP_LOG_VERBOSE")), "true"),
		MailFailEnabled:     strings.EqualFold(strings.TrimSpace(os.Getenv("MAILTAIL_MAILFAIL_ENABLED")), "true"),
		MailFailRulesFile:   strings.TrimSpace(os.Getenv("MAILTAIL_MAILFAIL_RULES_FILE")),
		AllowedRemoteIPs:    strings.TrimSpace(os.Getenv("MAILTAIL_ALLOWED_REMOTE_IPS")),
		AcceptedRcptDomains: strings.TrimSpace(os.Getenv("MAILTAIL_ACCEPTED_RCPT_DOMAINS")),
		AcceptedFromDomains: strings.TrimSpace(os.Getenv("MAILTAIL_ACCEPTED_FROM_DOMAINS")),
	}
}

func logSettings(logger *log.Logger, settings models.AppSettings) {
	if strings.TrimSpace(settings.AcceptedRcptDomains) == "" {
		logger.Printf("warning: MAILTAIL_ACCEPTED_RCPT_DOMAINS is empty, accepting RCPT TO for all domains")
	} else {
		logger.Printf("accepted RCPT TO patterns: %s", settings.AcceptedRcptDomains)
	}
	if strings.TrimSpace(settings.AcceptedFromDomains) == "" {
		logger.Printf("warning: MAILTAIL_ACCEPTED_FROM_DOMAINS is empty, accepting MAIL FROM for all domains")
	} else {
		logger.Printf("accepted MAIL FROM patterns: %s", settings.AcceptedFromDomains)
	}
	if strings.TrimSpace(settings.AllowedRemoteIPs) == "" {
		logger.Printf("warning: MAILTAIL_ALLOWED_REMOTE_IPS is empty, accepting SMTP connections from all remote IPs")
	} else {
		logger.Printf("accepted SMTP remote IPs: %s", settings.AllowedRemoteIPs)
	}
	if strings.TrimSpace(settings.AllowedOrigins) == "" {
		logger.Printf("CORS disabled for cross-origin browsers; web UI and API are same-origin by default")
	} else {
		logger.Printf("allowed CORS origins: %s", settings.AllowedOrigins)
	}
	if settings.SMTPLogVerbose {
		logger.Printf("verbose SMTP logging enabled")
	}
	if settings.MailFailEnabled {
		logger.Printf("mailfail enabled with rules file %s", settings.MailFailRulesFile)
	} else {
		logger.Printf("mailfail disabled")
	}
}
