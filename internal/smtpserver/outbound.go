package smtpserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/storage"
)

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
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
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
	defer client.Close()
	if err := client.Hello(s.config.Helo); err != nil {
		return err
	}
	if s.config.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("outbound SMTP relay does not advertise STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if s.config.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.config.Username, s.config.Password, host)); err != nil {
			return err
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

func RunOutboundWorker(ctx context.Context, logger *log.Logger, store storage.Store, sender *RelaySender) {
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
