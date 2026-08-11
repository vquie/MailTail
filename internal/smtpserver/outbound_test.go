package smtpserver

import (
	"bufio"
	"context"
	"fmt"
	"net"
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

func TestOutboundRetryDelayIsCapped(t *testing.T) {
	t.Parallel()

	if got := outboundRetryDelay(1); got != time.Minute {
		t.Fatalf("unexpected first delay: %s", got)
	}
	if got := outboundRetryDelay(100); got != time.Hour {
		t.Fatalf("unexpected capped delay: %s", got)
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
