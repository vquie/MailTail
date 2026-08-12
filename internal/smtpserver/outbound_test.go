package smtpserver

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/vquie/MailTail/internal/models"
)

func TestRelaySenderDeliversNullEnvelopeBounce(t *testing.T) {
	t.Parallel()

	clientConnection, serverConnection := net.Pipe()
	commands := make(chan []string, 1)
	go serveSMTPTestConnection(serverConnection, commands)

	sender, err := NewRelaySender(RelayConfig{Address: "127.0.0.1:25", TLSMode: "none", Helo: "mailtail.test"})
	if err != nil {
		t.Fatalf("create relay sender: %v", err)
	}
	sender.dialContext = func(context.Context, string, string) (net.Conn, error) {
		return clientConnection, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sender.Send(ctx, models.OutboundMessage{
		EnvelopeFrom: "",
		Recipient:    "sender@example.test",
		Raw:          "From: postmaster@example.test\r\nTo: sender@example.test\r\nSubject: failure\r\n\r\nbody\r\n",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case received := <-commands:
		joined := strings.Join(received, "\n")
		for _, want := range []string{"EHLO mailtail.test", "MAIL FROM:<>", "RCPT TO:<sender@example.test>", "Subject: failure"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %q in SMTP exchange:\n%s", want, joined)
			}
		}
	case <-ctx.Done():
		t.Fatal("SMTP test server did not finish")
	}
}

func TestOutboundRejectsEightBitReportWithoutEightBitMIME(t *testing.T) {
	t.Parallel()

	clientConnection, serverConnection := net.Pipe()
	commands := make(chan []string, 1)
	go serveSMTPTestConnection(serverConnection, commands)

	sender, err := NewRelaySender(RelayConfig{Address: "127.0.0.1:25", TLSMode: "none", Helo: "mailtail.test"})
	if err != nil {
		t.Fatalf("create relay sender: %v", err)
	}
	sender.dialContext = func(context.Context, string, string) (net.Conn, error) {
		return clientConnection, nil
	}
	err = sender.Send(context.Background(), models.OutboundMessage{
		EnvelopeFrom: "reports@example.test",
		Recipient:    "sender@example.test",
		Raw:          "From: reports@example.test\r\nTo: sender@example.test\r\nSubject: test\r\n\r\nGrüße\r\n",
	})
	if err == nil || !strings.Contains(err.Error(), "8BITMIME") {
		t.Fatalf("expected 8BITMIME capability failure, got %v", err)
	}
}

func TestOutboundTLSConfigOnlySkipsVerificationWhenRequested(t *testing.T) {
	t.Parallel()

	if strict := outboundTLSConfig("relay.test", false); strict.InsecureSkipVerify {
		t.Fatal("strict relay TLS config must validate certificates")
	}
	if opportunistic := outboundTLSConfig("mx.sender.test", true); !opportunistic.InsecureSkipVerify {
		t.Fatal("opportunistic direct TLS config must allow invalid certificates")
	}
}

func TestDirectSenderUsesMXPriorityAndFailover(t *testing.T) {
	t.Parallel()

	clientConnection, serverConnection := net.Pipe()
	commands := make(chan []string, 1)
	go serveSMTPTestConnection(serverConnection, commands)

	sender := NewDirectSender("mx.mailtail.test")
	sender.lookupMX = func(_ context.Context, domain string) ([]*net.MX, error) {
		if domain != "sender.test" {
			t.Fatalf("unexpected MX lookup domain: %q", domain)
		}
		return []*net.MX{
			{Host: "mx2.sender.test.", Pref: 20},
			{Host: "mx1.sender.test.", Pref: 10},
		}, nil
	}
	var attempted []string
	sender.dialContext = func(_ context.Context, _, address string) (net.Conn, error) {
		attempted = append(attempted, address)
		if address == "mx1.sender.test:25" {
			return nil, fmt.Errorf("first MX unavailable")
		}
		if address == "mx2.sender.test:25" {
			return clientConnection, nil
		}
		return nil, fmt.Errorf("unexpected target %s", address)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sender.Send(ctx, models.OutboundMessage{
		EnvelopeFrom: "",
		Recipient:    "sender@sender.test",
		Raw:          "From: postmaster@example.test\r\nTo: sender@sender.test\r\nSubject: failure\r\n\r\nbody\r\n",
	}); err != nil {
		t.Fatalf("direct send: %v", err)
	}
	if got := strings.Join(attempted, ","); got != "mx1.sender.test:25,mx2.sender.test:25" {
		t.Fatalf("unexpected MX attempt order: %s", got)
	}

	select {
	case received := <-commands:
		joined := strings.Join(received, "\n")
		for _, want := range []string{"EHLO mx.mailtail.test", "MAIL FROM:<>", "RCPT TO:<sender@sender.test>"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %q in SMTP exchange:\n%s", want, joined)
			}
		}
	case <-ctx.Done():
		t.Fatal("SMTP test server did not finish")
	}
}

func TestDirectSenderAcceptsInvalidCertificateForOpportunisticTLS(t *testing.T) {
	t.Parallel()

	clientConnection, serverConnection := net.Pipe()
	commands := make(chan []string, 1)
	certificate := newSMTPTestCertificate(t, "wrong-name.sender.test")
	go serveSMTPStartTLSTestConnection(serverConnection, certificate, commands)

	sender := NewDirectSender("mx.mailtail.test")
	sender.lookupMX = func(context.Context, string) ([]*net.MX, error) {
		return []*net.MX{{Host: "mx.sender.test.", Pref: 10}}, nil
	}
	sender.dialContext = func(context.Context, string, string) (net.Conn, error) {
		return clientConnection, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sender.Send(ctx, models.OutboundMessage{
		EnvelopeFrom: "",
		Recipient:    "sender@sender.test",
		Raw:          "From: postmaster@example.test\r\nTo: sender@sender.test\r\nSubject: failure\r\n\r\nbody\r\n",
	}); err != nil {
		t.Fatalf("direct send with invalid opportunistic TLS certificate: %v", err)
	}

	select {
	case received := <-commands:
		joined := strings.Join(received, "\n")
		for _, want := range []string{"STARTTLS", "MAIL FROM:<>", "RCPT TO:<sender@sender.test>"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %q in SMTP exchange:\n%s", want, joined)
			}
		}
	case <-ctx.Done():
		t.Fatal("SMTP STARTTLS test server did not finish")
	}
}

func TestDirectSenderFallsBackToRecipientDomainWithoutMX(t *testing.T) {
	t.Parallel()

	sender := NewDirectSender("mx.mailtail.test")
	sender.lookupMX = func(context.Context, string) ([]*net.MX, error) {
		return nil, nil
	}
	var target string
	sender.dialContext = func(_ context.Context, _, address string) (net.Conn, error) {
		target = address
		return nil, fmt.Errorf("unavailable")
	}
	err := sender.Send(context.Background(), models.OutboundMessage{Recipient: "sender@example.test"})
	if err == nil {
		t.Fatal("expected direct delivery error")
	}
	if target != "example.test:25" {
		t.Fatalf("unexpected address fallback: %q", target)
	}
}

func TestDirectSenderHonorsNullMX(t *testing.T) {
	t.Parallel()

	sender := NewDirectSender("mx.mailtail.test")
	sender.lookupMX = func(context.Context, string) ([]*net.MX, error) {
		return []*net.MX{{Host: ".", Pref: 0}}, nil
	}
	sender.dialContext = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("null MX must not be dialed")
		return nil, nil
	}
	err := sender.Send(context.Background(), models.OutboundMessage{Recipient: "sender@example.test"})
	if err == nil || !strings.Contains(err.Error(), "null MX") {
		t.Fatalf("expected null MX error, got %v", err)
	}
}

func TestOutboundRetryDelayIsCapped(t *testing.T) {
	t.Parallel()

	if got := outboundRetryDelay(1); got != time.Minute {
		t.Fatalf("unexpected first delay: %s", got)
	}
	if got := outboundRetryDelay(100); got != time.Hour {
		t.Fatalf("unexpected capped delay: %s", got)
	}
}

func TestDirectSenderClassifiesEndOfDATATemporaryFailure(t *testing.T) {
	t.Parallel()

	clientConnection, serverConnection := net.Pipe()
	commands := make(chan []string, 1)
	go serveSMTPTemporaryFailure(serverConnection, commands)

	sender := NewDirectSender("mx.mailtail.test")
	sender.lookupMX = func(context.Context, string) ([]*net.MX, error) {
		return []*net.MX{{Host: "mx.sender.test.", Pref: 10}}, nil
	}
	sender.dialContext = func(context.Context, string, string) (net.Conn, error) {
		return clientConnection, nil
	}
	err := sender.Send(context.Background(), models.OutboundMessage{
		EnvelopeFrom: "reports@mailtail.test",
		Recipient:    "fbl@sender.test",
		Raw:          "From: reports@mailtail.test\r\nTo: fbl@sender.test\r\nSubject: report\r\n\r\nbody\r\n",
	})
	if err == nil || !isTemporaryOutboundError(err) {
		t.Fatalf("expected retryable 421 error, got %v", err)
	}
	select {
	case <-commands:
	case <-time.After(time.Second):
		t.Fatal("SMTP test server did not receive DATA")
	}
}

func TestOutboundBatchDefersSameDomainAfterThrottle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 12, 12, 10, 35, 0, time.UTC)
	queue := &outboundQueueStub{messages: []models.OutboundMessage{
		{ID: 1, Recipient: "fbl@sender.test"},
		{ID: 2, Recipient: "other@sender.test"},
		{ID: 3, Recipient: "fbl@other.test"},
	}}
	var sent []int64
	sender := outboundSenderFunc(func(_ context.Context, message models.OutboundMessage) error {
		sent = append(sent, message.ID)
		if message.ID == 1 {
			return &textproto.Error{Code: 421, Msg: "4.3.5 End-of-DATA rejected: Try later"}
		}
		return nil
	})

	runOutboundBatch(context.Background(), log.New(io.Discard, "", 0), queue, sender, now)

	if got := fmt.Sprint(sent); got != "[1 3]" {
		t.Fatalf("same-domain message should be deferred without another SMTP attempt, sent=%s", got)
	}
	first := queue.rescheduled[1]
	second := queue.rescheduled[2]
	if first.attempts != 1 || second.attempts != 0 {
		t.Fatalf("unexpected attempt counters: first=%d second=%d", first.attempts, second.attempts)
	}
	if !first.nextAttempt.Equal(now.Add(time.Minute)) || !second.nextAttempt.Equal(first.nextAttempt) {
		t.Fatalf("same-domain messages should share retry time: first=%s second=%s", first.nextAttempt, second.nextAttempt)
	}
	if got := fmt.Sprint(queue.deferredDomains); got != "[sender.test]" {
		t.Fatalf("temporary failure should persist domain deferral, got %s", got)
	}
	if got := fmt.Sprint(queue.deleted); got != "[3]" {
		t.Fatalf("unexpected completed messages: %s", got)
	}
}

type outboundSenderFunc func(context.Context, models.OutboundMessage) error

func (f outboundSenderFunc) Send(ctx context.Context, message models.OutboundMessage) error {
	return f(ctx, message)
}

type outboundReschedule struct {
	attempts    int
	nextAttempt time.Time
	lastError   string
}

type outboundQueueStub struct {
	messages        []models.OutboundMessage
	deleted         []int64
	rescheduled     map[int64]outboundReschedule
	deferredDomains []string
}

func (s *outboundQueueStub) DeferOutboundMessagesForDomain(_ context.Context, domain string, exceptID int64, nextAttempt time.Time, lastError string) error {
	s.deferredDomains = append(s.deferredDomains, domain)
	for _, message := range s.messages {
		messageDomain, ok := extractDomain(message.Recipient)
		if message.ID == exceptID || !ok || messageDomain != domain {
			continue
		}
		current := s.rescheduled[message.ID]
		if current.nextAttempt.IsZero() || current.nextAttempt.Before(nextAttempt) {
			current.nextAttempt = nextAttempt
		}
		current.lastError = lastError
		s.rescheduled[message.ID] = current
	}
	return nil
}

func (s *outboundQueueStub) ListDueOutboundMessages(context.Context, time.Time, int) ([]models.OutboundMessage, error) {
	return append([]models.OutboundMessage(nil), s.messages...), nil
}

func (s *outboundQueueStub) DeleteOutboundMessage(_ context.Context, id int64) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *outboundQueueStub) RescheduleOutboundMessage(_ context.Context, id int64, attempts int, nextAttempt time.Time, lastError string) error {
	if s.rescheduled == nil {
		s.rescheduled = make(map[int64]outboundReschedule)
	}
	s.rescheduled[id] = outboundReschedule{attempts: attempts, nextAttempt: nextAttempt, lastError: lastError}
	return nil
}

func serveSMTPTemporaryFailure(connection net.Conn, commands chan<- []string) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	write := func(value string) {
		_, _ = fmt.Fprintf(writer, "%s\r\n", value)
		_ = writer.Flush()
	}
	write("220 smtp.test ready")

	var received []string
	data := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		received = append(received, line)
		if data {
			if line == "." {
				write("421 4.3.5 End-of-DATA rejected: Try later")
				commands <- received
				return
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "EHLO "):
			write("250 smtp.test")
		case strings.HasPrefix(line, "MAIL FROM:"):
			write("250 sender ok")
		case strings.HasPrefix(line, "RCPT TO:"):
			write("250 recipient ok")
		case line == "DATA":
			data = true
			write("354 send data")
		default:
			write("500 unsupported")
		}
	}
}

func serveSMTPTestConnection(connection net.Conn, commands chan<- []string) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	write := func(value string) {
		_, _ = fmt.Fprintf(writer, "%s\r\n", value)
		_ = writer.Flush()
	}
	write("220 smtp.test ready")

	var received []string
	data := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		received = append(received, line)
		if data {
			if line == "." {
				data = false
				write("250 queued")
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "EHLO "):
			write("250 smtp.test")
		case strings.HasPrefix(line, "MAIL FROM:"):
			write("250 sender ok")
		case strings.HasPrefix(line, "RCPT TO:"):
			write("250 recipient ok")
		case line == "DATA":
			data = true
			write("354 send data")
		case line == "QUIT":
			write("221 bye")
			commands <- received
			return
		default:
			write("500 unsupported")
		}
	}
}

func serveSMTPStartTLSTestConnection(connection net.Conn, certificate tls.Certificate, commands chan<- []string) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	write := func(value string) {
		_, _ = fmt.Fprintf(writer, "%s\r\n", value)
		_ = writer.Flush()
	}
	write("220 smtp.test ready")

	var received []string
	data := false
	encrypted := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		received = append(received, line)
		if data {
			if line == "." {
				data = false
				write("250 queued")
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "EHLO ") && !encrypted:
			write("250-smtp.test")
			write("250 STARTTLS")
		case line == "STARTTLS" && !encrypted:
			write("220 begin TLS")
			tlsConnection := tls.Server(connection, &tls.Config{
				Certificates: []tls.Certificate{certificate},
				MinVersion:   tls.VersionTLS12,
			})
			if err := tlsConnection.Handshake(); err != nil {
				return
			}
			encrypted = true
			reader = bufio.NewReader(tlsConnection)
			writer = bufio.NewWriter(tlsConnection)
		case strings.HasPrefix(line, "EHLO "):
			write("250 smtp.test")
		case strings.HasPrefix(line, "MAIL FROM:"):
			write("250 sender ok")
		case strings.HasPrefix(line, "RCPT TO:"):
			write("250 recipient ok")
		case line == "DATA":
			data = true
			write("354 send data")
		case line == "QUIT":
			write("221 bye")
			commands <- received
			return
		default:
			write("500 unsupported")
		}
	}
}

func newSMTPTestCertificate(t *testing.T, dnsName string) tls.Certificate {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test TLS key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create test TLS certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}
}
