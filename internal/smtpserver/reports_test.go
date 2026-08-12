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
		{action: "original-report", contentType: "multipart/report", want: []string{"report-type=feedback-report", "Feedback-Type: other", "Content-Type: message/rfc822", "Hello"}},
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
			if test.action != "async-bounce" && !strings.Contains(message.Raw, "Subject: test\r\n") {
				t.Fatal("feedback report must preserve the original subject")
			}
			for _, want := range test.want {
				if !strings.Contains(message.Raw, want) {
					t.Fatalf("missing %q in report", want)
				}
			}
		})
	}
}

func TestFeedbackReportsMatchHalonParserMIMEContract(t *testing.T) {
	t.Parallel()

	original := []byte("From: sender@sender.test\r\nTo: trap@example.test\r\nSubject: test\r\n\r\nbody\r\n")
	tests := []struct {
		action       string
		feedbackType string
		wantFeedback string
		wantParts    []string
	}{
		{action: "arf", feedbackType: "fraud", wantFeedback: "fraud", wantParts: []string{"text/plain", "message/feedback-report", "message/rfc822"}},
		{action: "xarf-v3", wantFeedback: "xarf", wantParts: []string{"text/plain", "message/feedback-report", "application/json"}},
		{action: "xarf-v4", wantFeedback: "xarf", wantParts: []string{"text/plain", "message/feedback-report", "application/json"}},
		{action: "original-report", wantFeedback: "other", wantParts: []string{"text/plain", "message/feedback-report", "message/rfc822"}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.action, func(t *testing.T) {
			t.Parallel()

			message, err := BuildReportMessage(ReportRequest{
				Action:       test.action,
				Recipient:    "trap@example.test",
				From:         "reports@example.test",
				FeedbackType: test.feedbackType,
				Message:      "Automated report",
			}, SessionMetadata{RemoteIP: "192.0.2.55", MailFrom: "sender@sender.test"}, original, time.Now().UTC())
			if err != nil {
				t.Fatalf("build report: %v", err)
			}

			header, parts := parseMultipartReport(t, message.Raw)
			mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
			if err != nil {
				t.Fatalf("parse outer content type: %v", err)
			}
			if mediaType != "multipart/report" || params["report-type"] != "feedback-report" {
				t.Fatalf("unexpected outer content type: %q", header.Get("Content-Type"))
			}
			if len(parts) != len(test.wantParts) {
				t.Fatalf("unexpected MIME part count: got %d want %d", len(parts), len(test.wantParts))
			}
			for index, want := range test.wantParts {
				partType, _, err := mime.ParseMediaType(parts[index].header.Get("Content-Type"))
				if err != nil {
					t.Fatalf("parse part %d content type: %v", index, err)
				}
				if partType != want {
					t.Fatalf("part %d has type %q, want %q", index, partType, want)
				}
			}

			feedback := parts[1].body
			for _, field := range []string{
				"Feedback-Type: " + test.wantFeedback,
				"User-Agent: " + reportUserAgent,
				"Version: 1",
			} {
				if !strings.Contains(feedback, field) {
					t.Fatalf("feedback report missing %q: %s", field, feedback)
				}
			}
		})
	}
}

type parsedReportPart struct {
	header textproto.MIMEHeader
	body   string
}

func parseMultipartReport(t *testing.T, raw string) (textproto.MIMEHeader, []parsedReportPart) {
	t.Helper()

	reader := textproto.NewReader(bufio.NewReader(strings.NewReader(raw)))
	header, err := reader.ReadMIMEHeader()
	if err != nil {
		t.Fatalf("read message header: %v", err)
	}
	_, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	multipartReader := multipart.NewReader(reader.R, params["boundary"])
	parts := make([]parsedReportPart, 0, 4)
	for {
		part, err := multipartReader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read MIME part: %v", err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read MIME part body: %v", err)
		}
		parts = append(parts, parsedReportPart{header: part.Header, body: string(body)})
	}
	return header, parts
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
	if requests[0].Message != "Unknown user" {
		t.Fatalf("custom async bounce text was not retained: %#v", requests[0])
	}
}

func TestAsyncBounceUsesCustomDiagnosticText(t *testing.T) {
	t.Parallel()

	message, err := BuildReportMessage(ReportRequest{
		Action:       "async-bounce",
		Recipient:    "quota@example.test",
		From:         "postmaster@example.test",
		Code:         552,
		EnhancedCode: "5.2.2",
		Message:      "Mailbox quota exceeded",
	}, SessionMetadata{MailFrom: "sender@sender.test"}, []byte("Subject: quota\r\n\r\nbody"), time.Now().UTC())
	if err != nil {
		t.Fatalf("build async bounce: %v", err)
	}
	for _, want := range []string{
		"Mailbox quota exceeded\r\n",
		"Diagnostic-Code: smtp; 552 5.2.2 Mailbox quota exceeded",
	} {
		if !strings.Contains(message.Raw, want) {
			t.Fatalf("async bounce missing %q", want)
		}
	}
}

func TestMailFailARFFeedbackType(t *testing.T) {
	t.Parallel()

	engine, err := NewMailFailEngine([]models.MailFailRule{{
		Name: "fraud", Trigger: "fraud", Action: "arf", FeedbackType: "fraud",
	}}, nil)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	requests := engine.MatchReports([]string{"user+fraud@example.test"})
	if len(requests) != 1 || requests[0].FeedbackType != "fraud" {
		t.Fatalf("unexpected report request: %#v", requests)
	}

	_, err = NewMailFailEngine([]models.MailFailRule{{
		Name: "invalid", Trigger: "invalid", Action: "arf", FeedbackType: "xarf",
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported ARF feedback type") {
		t.Fatalf("expected feedback type validation error, got %v", err)
	}
}

func TestMailFailReportRulesDoNotRequireStageOrMessage(t *testing.T) {
	t.Parallel()

	actions := []string{"arf", "xarf-v3", "xarf-v4", "original-report", "async-bounce"}
	for _, action := range actions {
		action := action
		t.Run(action, func(t *testing.T) {
			t.Parallel()

			engine, err := NewMailFailEngine([]models.MailFailRule{{
				Name: action, Trigger: action, Action: action,
			}}, nil)
			if err != nil {
				t.Fatalf("create engine without stage or message: %v", err)
			}

			recipient := "user+" + action + "@example.test"
			if response := engine.MatchData("sender@sender.test", []string{recipient}); response != nil {
				t.Fatalf("report rule must not reject DATA: %v", response)
			}
			requests := engine.MatchReports([]string{recipient})
			if len(requests) != 1 {
				t.Fatalf("expected one report request, got %#v", requests)
			}
			if requests[0].Action != action || requests[0].Message == "" {
				t.Fatalf("unexpected report request: %#v", requests[0])
			}
			if action == "async-bounce" && (requests[0].Code != 550 || requests[0].EnhancedCode != "5.0.0") {
				t.Fatalf("unexpected default bounce diagnostic: %#v", requests[0])
			}
		})
	}
}
