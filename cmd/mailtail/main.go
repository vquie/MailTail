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

	authUsername := strings.TrimSpace(os.Getenv("MAILTAIL_ADMIN_USERNAME"))
	authPassword := os.Getenv("MAILTAIL_ADMIN_PASSWORD")
	if authUsername == "" && strings.TrimSpace(os.Getenv("MAILTAIL_AUTH_USERNAME")) != "" {
		authUsername = strings.TrimSpace(os.Getenv("MAILTAIL_AUTH_USERNAME"))
		logger.Printf("warning: MAILTAIL_AUTH_USERNAME is deprecated, use MAILTAIL_ADMIN_USERNAME")
	}
	if authPassword == "" && os.Getenv("MAILTAIL_AUTH_PASSWORD") != "" {
		authPassword = os.Getenv("MAILTAIL_AUTH_PASSWORD")
		logger.Printf("warning: MAILTAIL_AUTH_PASSWORD is deprecated, use MAILTAIL_ADMIN_PASSWORD")
	}
	dataDir := getEnv("MAILTAIL_DATA_DIR", "data")
	httpAddr := getEnv("MAILTAIL_HTTP_ADDR", ":8080")
	smtpAddr := getEnv("MAILTAIL_SMTP_ADDR", ":8025")
	outboundMode := strings.ToLower(strings.TrimSpace(getEnv("MAILTAIL_OUTBOUND_MODE", "direct")))
	outboundSMTPAddr := strings.TrimSpace(os.Getenv("MAILTAIL_OUTBOUND_SMTP_ADDR"))
	outboundHelo := getEnv("MAILTAIL_OUTBOUND_SMTP_HELO", "mailtail.local")
	staticDir := getEnv("MAILTAIL_WEB_DIR", filepath.Join("web", "dist"))
	if strings.TrimSpace(os.Getenv("MAILTAIL_MAILFAIL_ENABLED")) != "" {
		logger.Printf("warning: MAILTAIL_MAILFAIL_ENABLED is deprecated and ignored; enable MailFail per user in the UI")
	}
	if strings.TrimSpace(os.Getenv("MAILTAIL_MAILFAIL_RULES_FILE")) != "" {
		logger.Printf("warning: MAILTAIL_MAILFAIL_RULES_FILE is deprecated and ignored; configure MailFail rules in the UI")
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		logger.Fatalf("create data dir: %v", err)
	}
	logger.Printf("version: %s", version)
	switch {
	case authUsername == "" && authPassword == "":
		logger.Printf("warning: HTTP auth is disabled, set MAILTAIL_ADMIN_USERNAME and MAILTAIL_ADMIN_PASSWORD to protect the web UI and API")
	case authUsername == "" || authPassword == "":
		logger.Fatal("invalid auth config: MAILTAIL_ADMIN_USERNAME and MAILTAIL_ADMIN_PASSWORD must either both be set or both be empty")
	default:
		logger.Printf("HTTP auth enabled for web UI and API")
	}
	store, err := storage.NewSQLiteStore(filepath.Join(dataDir, "mailtail.db"))
	if err != nil {
		logger.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	var outboundSender smtpserver.OutboundSender
	switch outboundMode {
	case "direct":
		outboundSender = smtpserver.NewDirectSender(outboundHelo)
		logger.Printf("outbound report delivery mode: direct-to-MX")
	case "relay":
		if outboundSMTPAddr == "" {
			logger.Fatal("invalid outbound SMTP config: MAILTAIL_OUTBOUND_SMTP_ADDR is required in relay mode")
		}
		outboundSender, err = smtpserver.NewRelaySender(smtpserver.RelayConfig{
			Address:  outboundSMTPAddr,
			TLSMode:  getEnv("MAILTAIL_OUTBOUND_SMTP_TLS", "starttls"),
			Username: os.Getenv("MAILTAIL_OUTBOUND_SMTP_USERNAME"),
			Password: os.Getenv("MAILTAIL_OUTBOUND_SMTP_PASSWORD"),
			Helo:     outboundHelo,
		})
		if err != nil {
			logger.Fatalf("invalid outbound SMTP config: %v", err)
		}
		logger.Printf("outbound report delivery mode: relay via %s", outboundSMTPAddr)
	default:
		logger.Fatalf("invalid outbound mode %q: MAILTAIL_OUTBOUND_MODE must be direct or relay", outboundMode)
	}

	settings := envAppSettings()
	if savedSettings, ok, err := store.LoadAppSettings(context.Background()); err != nil {
		logger.Fatalf("load app settings: %v", err)
	} else if ok {
		settings = sanitizeBootstrapSettings(savedSettings)
		logger.Printf("loaded app settings from database")
	}
	settings = sanitizeBootstrapSettings(settings)
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

	go runMessageRetentionWorker(ctx, logger, store, runtime)
	go smtpserver.RunOutboundWorker(ctx, logger, store, outboundSender)

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
		AllowedRemoteIPs:    strings.TrimSpace(os.Getenv("MAILTAIL_ALLOWED_REMOTE_IPS")),
		AcceptedRcptDomains: strings.TrimSpace(os.Getenv("MAILTAIL_ACCEPTED_RCPT_DOMAINS")),
		AcceptedFromDomains: strings.TrimSpace(os.Getenv("MAILTAIL_ACCEPTED_FROM_DOMAINS")),
	}
}

func sanitizeBootstrapSettings(settings models.AppSettings) models.AppSettings {
	settings.MailFailEnabled = false
	settings.MailFailRules = nil
	return settings
}

func logSettings(logger *log.Logger, settings models.AppSettings) {
	if strings.TrimSpace(settings.AcceptedRcptDomains) == "" {
		logger.Printf("default recipient domain restrictions: none configured")
	} else {
		logger.Printf("default recipient domain restrictions: %s", settings.AcceptedRcptDomains)
	}
	if strings.TrimSpace(settings.AcceptedFromDomains) == "" {
		logger.Printf("default sender domain restrictions: none configured")
	} else {
		logger.Printf("default sender domain restrictions: %s", settings.AcceptedFromDomains)
	}
	if strings.TrimSpace(settings.AllowedRemoteIPs) == "" {
		logger.Printf("default SMTP remote IP restrictions: none configured")
	} else {
		logger.Printf("default SMTP remote IP restrictions: %s", settings.AllowedRemoteIPs)
	}
	if strings.TrimSpace(settings.AllowedOrigins) == "" {
		logger.Printf("cross-origin browser access: disabled by default")
	} else {
		logger.Printf("cross-origin browser access allow-list: %s", settings.AllowedOrigins)
	}
	if settings.SMTPLogVerbose {
		logger.Printf("verbose SMTP logging enabled")
	}
	if settings.AutoDeleteAfterDays > 0 {
		logger.Printf("message auto-delete enabled after %d day(s)", settings.AutoDeleteAfterDays)
	}
}

func runMessageRetentionWorker(ctx context.Context, logger *log.Logger, store storage.Store, runtime *runtimeconfig.Manager) {
	runRetentionPass := func() {
		before := time.Now().UTC()
		deleted, err := store.DeleteExpiredMessages(context.Background(), before)
		if err != nil {
			logger.Printf("message retention failed: %v", err)
			return
		}
		if deleted > 0 {
			logger.Printf("message retention deleted %d expired message(s)", deleted)
		}
	}

	runRetentionPass()

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runRetentionPass()
		}
	}
}
