package smtpserver

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/vquie/MailTail/internal/models"
)

func TestBuildReportMessageFormats(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	session := SessionMetadata{
		RemoteIP: "192.0.2.55",
		MailFrom: "sender@sender.test",
		RcptTo:   []string{"user+report@example.test"},
	}
	original := []byte("From: sender@sender.test\r\nTo: user@example.test\r\nSubject: test\r\n\r\nHello\r\n")

	tests := []struct {
		action      string
		contentType string
		want        []string
	}{
		{action: "arf", contentType: "multipart/report", want: []string{"report-type=feedback-report", "Feedback-Type: abuse", "Content-Type: message/rfc822"}},
		{action: "xarf-v3", contentType: "multipart/report", want: []string{"Feedback-Type: xarf", "application/json; name=xarf.json"}},
		{action: "xarf-v4", contentType: "multipart/report", want: []string{"Feedback-Type: xarf", "application/json; name=xarf.json"}},
		{action: "original-report", contentType: "multipart/mixed", want: []string{"Content-Type: message/rfc822", "Hello"}},
		{action: "async-bounce", contentType: "multipart/report", want: []string{"report-type=delivery-status", "Content-Type: message/delivery-status", "Action: failed", "Status: 5.1.1"}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.action, func(t *testing.T) {
			t.Parallel()
			message, err := BuildReportMessage(ReportRequest{
				Action:       test.action,
				Recipient:    session.RcptTo[0],
				From:         "postmaster@example.test",
				Code:         550,
				EnhancedCode: "5.1.1",
				Message:      "Generated test report",
			}, session, original, now)
			if err != nil {
				t.Fatalf("build report: %v", err)
			}
			if message.Recipient != session.MailFrom {
				t.Fatalf("unexpected recipient: %q", message.Recipient)
			}
			if test.action == "async-bounce" && message.EnvelopeFrom != "" {
				t.Fatalf("async bounce envelope sender must be empty, got %q", message.EnvelopeFrom)
			}
			if test.action != "async-bounce" && message.EnvelopeFrom != "postmaster@example.test" {
				t.Fatalf("unexpected envelope sender: %q", message.EnvelopeFrom)
			}
			if !strings.Contains(message.Raw, "Content-Type: "+test.contentType) {
				t.Fatalf("missing outer content type in:\n%s", message.Raw)
			}
			if !strings.Contains(message.Raw, "Auto-Submitted: auto-generated") {
				t.Fatal("missing loop prevention header")
			}
			for _, want := range test.want {
				if !strings.Contains(message.Raw, want) {
					t.Fatalf("missing %q in report", want)
				}
			}
		})
	}
}

func TestBuildXARFJSONMatchesRequestedVersion(t *testing.T) {
	t.Parallel()

	session := SessionMetadata{RemoteIP: "192.0.2.55", MailFrom: "sender@sender.test"}
	request := ReportRequest{From: "reports@example.test", Recipient: "trap@example.test"}
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	original := []byte("Subject: test\r\n\r\nbody")

	for _, version := range []int{3, 4} {
		payload, err := buildXARFPayload(request, session, original, now, version)
		if err != nil {
			t.Fatalf("build XARF v%d: %v", version, err)
		}
		var value map[string]any
		if err := json.Unmarshal(payload, &value); err != nil {
			t.Fatalf("decode XARF v%d: %v", version, err)
		}
		if version == 3 && value["Version"] != "3" {
			t.Fatalf("unexpected v3 payload: %s", payload)
		}
		if version == 4 && value["xarf_version"] != "4.0.0" {
			t.Fatalf("unexpected v4 payload: %s", payload)
		}
	}
}

func TestXARFEmailAttachmentContainsJSON(t *testing.T) {
	t.Parallel()

	message, err := BuildReportMessage(ReportRequest{
		Action:    "xarf-v4",
		Recipient: "trap@example.test",
		From:      "reports@example.test",
		Message:   "Spam report",
	}, SessionMetadata{RemoteIP: "192.0.2.55", MailFrom: "sender@sender.test"}, []byte("Subject: test\r\n\r\nbody"), time.Now().UTC())
	if err != nil {
		t.Fatalf("build XARF report: %v", err)
	}

	reader := textproto.NewReader(bufio.NewReader(strings.NewReader(message.Raw)))
	header, err := reader.ReadMIMEHeader()
	if err != nil {
		t.Fatalf("read message header: %v", err)
	}
	_, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	multipartReader := multipart.NewReader(reader.R, params["boundary"])
	var decoded []byte
	for {
		part, err := multipartReader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read MIME part: %v", err)
		}
		if strings.HasPrefix(part.Header.Get("Content-Type"), "application/json") {
			encoded, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("read JSON part: %v", err)
			}
			decoded, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
			if err != nil {
				t.Fatalf("decode JSON part: %v", err)
			}
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(decoded, &payload); err != nil {
		t.Fatalf("decode attached JSON: %v", err)
	}
	if payload["category"] != "messaging" || payload["type"] != "spam" {
		t.Fatalf("unexpected XARF payload: %v", payload)
	}
}

func TestReportRuleSenderResolution(t *testing.T) {
	t.Parallel()

	rule := models.MailFailRule{Name: "arf", Trigger: "arf", Stage: "data", Action: "arf", Message: "Abuse report"}
	state, err := buildDomainPolicyState(1, DomainPolicyConfig{
		AcceptedRcptDomains: []string{"example.test"},
		MailFailEnabled:     true,
		MailFailRules:       []models.MailFailRule{rule},
	}, nil)
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}
	from, err := state.resolveReportFrom("user+arf@example.test")
	if err != nil {
		t.Fatalf("resolve report sender: %v", err)
	}
	if from != "postmaster@example.test" {
		t.Fatalf("unexpected derived sender: %q", from)
	}

	_, err = buildDomainPolicyState(1, DomainPolicyConfig{
		AcceptedRcptDomains: []string{"^.+@example\\.test$"},
		MailFailEnabled:     true,
		MailFailRules:       []models.MailFailRule{rule},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "reportFrom is required") {
		t.Fatalf("expected explicit sender validation error, got %v", err)
	}
}

func TestMailFailReportRulesArePostAcceptActions(t *testing.T) {
	t.Parallel()

	engine, err := NewMailFailEngine([]models.MailFailRule{{
		Name: "bounce", Trigger: "bounce", Stage: "data", Action: "async-bounce", Code: 550, EnhancedCode: "5.1.1", Message: "Unknown user",
	}}, nil)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	if response := engine.MatchData("sender@sender.test", []string{"user+bounce@example.test"}); response != nil {
		t.Fatalf("report rule must not reject DATA: %v", response)
	}
	requests := engine.MatchReports([]string{"user+bounce@example.test"})
	if len(requests) != 1 || requests[0].Action != "async-bounce" {
		t.Fatalf("unexpected report requests: %#v", requests)
	}
}
