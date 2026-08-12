package smtpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/mail"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/storage"
)

type OutboundSender interface {
	Send(ctx context.Context, message models.OutboundMessage) error
}

type DirectSender struct {
	helo        string
	lookupMX    func(context.Context, string) ([]*net.MX, error)
	dialContext func(context.Context, string, string) (net.Conn, error)
}

func NewDirectSender(helo string) *DirectSender {
	helo = strings.TrimSpace(helo)
	if helo == "" {
		helo = "mailtail.local"
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	return &DirectSender{
		helo:        helo,
		lookupMX:    net.DefaultResolver.LookupMX,
		dialContext: dialer.DialContext,
	}
}

func (s *DirectSender) Send(ctx context.Context, message models.OutboundMessage) error {
	recipient, err := mail.ParseAddress(strings.TrimSpace(message.Recipient))
	if err != nil || recipient.Address != strings.TrimSpace(message.Recipient) {
		return fmt.Errorf("invalid outbound recipient")
	}
	domain, ok := extractDomain(recipient.Address)
	if !ok {
		return fmt.Errorf("invalid outbound recipient domain")
	}

	mxRecords, err := s.lookupMX(ctx, domain)
	if err != nil {
		var dnsError *net.DNSError
		if !errors.As(err, &dnsError) || !dnsError.IsNotFound {
			return fmt.Errorf("MX lookup for %s failed: %w", domain, err)
		}
		mxRecords = nil
	}
	if len(mxRecords) == 0 {
		mxRecords = []*net.MX{{Host: domain, Pref: 0}}
	}
	sort.SliceStable(mxRecords, func(left, right int) bool {
		return mxRecords[left].Pref < mxRecords[right].Pref
	})

	failures := make([]string, 0, len(mxRecords))
	for _, record := range mxRecords {
		host := strings.TrimSuffix(strings.TrimSpace(record.Host), ".")
		if host == "" {
			return fmt.Errorf("recipient domain %s publishes a null MX and does not accept email", domain)
		}
		connection, err := s.dialContext(ctx, "tcp", net.JoinHostPort(host, "25"))
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", host, err))
			continue
		}
		err = deliverSMTP(ctx, connection, host, s.helo, "opportunistic", "", "", message)
		if err == nil {
			return nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", host, err))
	}
	return fmt.Errorf("direct delivery to %s failed: %s", domain, strings.Join(failures, "; "))
}

type RelayConfig struct {
	Address  string
	TLSMode  string
	Username string
	Password string
	Helo     string
}

type RelaySender struct {
	config      RelayConfig
	dialContext func(context.Context, string, string) (net.Conn, error)
}

func NewRelaySender(config RelayConfig) (*RelaySender, error) {
	config.Address = strings.TrimSpace(config.Address)
	config.TLSMode = strings.ToLower(strings.TrimSpace(config.TLSMode))
	config.Username = strings.TrimSpace(config.Username)
	config.Helo = strings.TrimSpace(config.Helo)
	if config.Address == "" {
		return nil, fmt.Errorf("outbound SMTP address is required")
	}
	if _, _, err := net.SplitHostPort(config.Address); err != nil {
		return nil, fmt.Errorf("invalid outbound SMTP address: %w", err)
	}
	if config.TLSMode == "" {
		config.TLSMode = "starttls"
	}
	switch config.TLSMode {
	case "none", "starttls", "tls":
	default:
		return nil, fmt.Errorf("outbound SMTP TLS mode must be none, starttls, or tls")
	}
	if config.Helo == "" {
		config.Helo = "mailtail.local"
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	return &RelaySender{config: config, dialContext: dialer.DialContext}, nil
}

func (s *RelaySender) Send(ctx context.Context, message models.OutboundMessage) error {
	host, _, err := net.SplitHostPort(s.config.Address)
	if err != nil {
		return err
	}
	tlsConfig := outboundTLSConfig(host, false)
	var connection net.Conn
	if s.config.TLSMode == "tls" {
		dialer := &net.Dialer{Timeout: 30 * time.Second}
		connection, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", s.config.Address)
	} else {
		connection, err = s.dialContext(ctx, "tcp", s.config.Address)
	}
	if err != nil {
		return err
	}
	return deliverSMTP(ctx, connection, host, s.config.Helo, s.config.TLSMode, s.config.Username, s.config.Password, message)
}

func deliverSMTP(ctx context.Context, connection net.Conn, host, helo, tlsMode, username, password string, message models.OutboundMessage) error {
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	} else {
		_ = connection.SetDeadline(time.Now().Add(30 * time.Second))
	}

	client, err := smtp.NewClient(connection, host)
	if err != nil {
		return err
	}
	if err := client.Hello(helo); err != nil {
		return err
	}
	if tlsMode == "starttls" || tlsMode == "opportunistic" {
		startTLS, _ := client.Extension("STARTTLS")
		if tlsMode == "starttls" && !startTLS {
			return fmt.Errorf("outbound SMTP relay does not advertise STARTTLS")
		}
		if startTLS {
			tlsConfig := outboundTLSConfig(host, tlsMode == "opportunistic")
			if err := client.StartTLS(tlsConfig); err != nil {
				return err
			}
		}
	}
	if username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			return err
		}
	}
	if contains8Bit(message.Raw) {
		if supported, _ := client.Extension("8BITMIME"); !supported {
			return fmt.Errorf("outbound SMTP server does not advertise 8BITMIME required by the report")
		}
	}
	if err := client.Mail(message.EnvelopeFrom); err != nil {
		return err
	}
	if err := client.Rcpt(message.Recipient); err != nil {
		return err
	}
	data, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := io.WriteString(data, message.Raw); err != nil {
		_ = data.Close()
		return err
	}
	if err := data.Close(); err != nil {
		return err
	}
	_ = client.Quit()
	return nil
}

func contains8Bit(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] >= 128 {
			return true
		}
	}
	return false
}

func outboundTLSConfig(host string, allowInvalidCertificate bool) *tls.Config {
	return &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		// Direct-to-MX STARTTLS is opportunistic: retain transport encryption even
		// when the destination certificate cannot be authenticated. Relay TLS stays strict.
		InsecureSkipVerify: allowInvalidCertificate, //nolint:gosec // Intentional opportunistic SMTP semantics.
	}
}

func RunOutboundWorker(ctx context.Context, logger *log.Logger, store storage.Store, sender OutboundSender) {
	if sender == nil {
		return
	}
	run := func() {
		messages, err := store.ListDueOutboundMessages(ctx, time.Now().UTC(), 25)
		if err != nil {
			if ctx.Err() == nil {
				logger.Printf("outbound report queue lookup failed: %v", err)
			}
			return
		}
		for _, message := range messages {
			sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := sender.Send(sendCtx, message)
			cancel()
			if err == nil {
				if err := store.DeleteOutboundMessage(ctx, message.ID); err != nil {
					logger.Printf("outbound report completion failed id=%d: %v", message.ID, err)
					continue
				}
				logger.Printf("outbound report delivered id=%d recipient=%q", message.ID, sanitizeLogValue(message.Recipient))
				continue
			}
			attempts := message.Attempts + 1
			nextAttempt := time.Now().UTC().Add(outboundRetryDelay(attempts))
			lastError := sanitizeLogValue(err.Error())
			if err := store.RescheduleOutboundMessage(ctx, message.ID, attempts, nextAttempt, lastError); err != nil {
				logger.Printf("outbound report reschedule failed id=%d: %v", message.ID, err)
				continue
			}
			logger.Printf("outbound report delivery failed id=%d attempt=%d retry_at=%s error=%q", message.ID, attempts, nextAttempt.Format(time.RFC3339), lastError)
		}
	}

	run()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func outboundRetryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 7 {
		attempts = 7
	}
	delay := time.Minute * time.Duration(1<<(attempts-1))
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}
