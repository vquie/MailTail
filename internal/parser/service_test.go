package parser

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vquie/MailTail/internal/models"
)

func TestParseMultipartMessage(t *testing.T) {
	raw := strings.Join([]string{
		"From: =?UTF-8?Q?Alice_=C3=84?= <alice@example.test>",
		"To: Bob <bob@example.test>",
		"Subject: =?UTF-8?Q?Gr=C3=BC=C3=9Fe?=",
		"Message-ID: <message-1@example.test>",
		"Content-Type: multipart/mixed; boundary=outer",
		"",
		"--outer",
		"Content-Type: multipart/alternative; boundary=inner",
		"",
		"--inner",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"Hello=20plain!",
		"--inner",
		"Content-Type: text/html; charset=utf-8",
		"Content-Transfer-Encoding: base64",
		"",
		"PHA+SGVsbG8gSFRNTCE8L3A+",
		"--inner--",
		"--outer",
		"Content-Type: application/octet-stream",
		"Content-Disposition: attachment; filename=\"=?UTF-8?Q?pr=C3=BCfung.txt?=\"",
		"Content-Transfer-Encoding: base64",
		"Content-ID: <attachment-1>",
		"",
		"c2VjcmV0",
		"--outer--",
		"",
	}, "\r\n")

	got, err := NewService().Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.HeaderFrom != "Alice Ä <alice@example.test>" {
		t.Errorf("HeaderFrom = %q", got.HeaderFrom)
	}
	if got.HeaderTo != "Bob <bob@example.test>" {
		t.Errorf("HeaderTo = %q", got.HeaderTo)
	}
	if got.Subject != "Grüße" {
		t.Errorf("Subject = %q", got.Subject)
	}
	if got.MessageID != "<message-1@example.test>" {
		t.Errorf("MessageID = %q", got.MessageID)
	}
	if strings.TrimSpace(got.TextBody) != "Hello plain!" {
		t.Errorf("TextBody = %q", got.TextBody)
	}
	if strings.TrimSpace(got.HTMLBody) != "<p>Hello HTML!</p>" {
		t.Errorf("HTMLBody = %q", got.HTMLBody)
	}
	if got.Size != len(raw) || got.Raw != raw {
		t.Errorf("raw message metadata was not preserved")
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("len(Attachments) = %d, want 1", len(got.Attachments))
	}
	attachment := got.Attachments[0]
	if attachment.FileName != "prüfung.txt" || attachment.ContentType != "application/octet-stream" {
		t.Errorf("attachment metadata = %#v", attachment)
	}
	if attachment.ContentID != "attachment-1" || attachment.Inline {
		t.Errorf("attachment routing metadata = %#v", attachment)
	}
	if attachment.Size != len("secret") || !bytes.Equal(attachment.Content, []byte("secret")) {
		t.Errorf("attachment content = %q", attachment.Content)
	}
}

func TestParsePlainMessageWithoutContentType(t *testing.T) {
	raw := "From: sender@example.test\r\nSubject: plain\r\n\r\nbody"

	got, err := NewService().Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.TextBody != "body" {
		t.Errorf("TextBody = %q, want body", got.TextBody)
	}
}

func TestParseRejectsMalformedMessage(t *testing.T) {
	if _, err := NewService().Parse([]byte("not a valid message")); err == nil {
		t.Fatal("Parse() error = nil, want malformed message error")
	}
}

func TestParseHeadersAndNormalizeMessage(t *testing.T) {
	service := NewService()
	headers, err := service.ParseHeaders("Subject: =?UTF-8?Q?Gr=C3=BC=C3=9Fe?=\r\n\r\n")
	if err != nil {
		t.Fatalf("ParseHeaders() error = %v", err)
	}
	if len(headers) != 1 || headers[0].Value != "Grüße" {
		t.Fatalf("headers = %#v", headers)
	}

	message := models.Message{
		Subject:    "=?UTF-8?Q?Gr=C3=BC=C3=9Fe?=",
		HeaderFrom: "=?UTF-8?Q?Alice_=C3=84?= <alice@example.test>",
		Headers:    []models.Header{{Key: "X-Label", Value: "=?UTF-8?Q?Pr=C3=BCfung?="}},
		Attachments: []models.Attachment{{
			FileName: "=?UTF-8?Q?pr=C3=BCfung.txt?=",
		}},
	}
	service.NormalizeMessage(&message)

	if message.Subject != "Grüße" || message.HeaderFrom != "Alice Ä <alice@example.test>" {
		t.Errorf("normalized message = %#v", message)
	}
	if message.Headers[0].Value != "Prüfung" || message.Attachments[0].FileName != "prüfung.txt" {
		t.Errorf("normalized nested fields = %#v / %#v", message.Headers, message.Attachments)
	}
}
