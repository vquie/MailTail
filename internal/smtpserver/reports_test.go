package smtpserver

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vquie/MailTail/internal/models"
)

func TestBuildReportMessageFormats(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	session := SessionMetadata{
		RemoteIP:   "192.0.2.55",
		RemotePort: 54321,
		MailFrom:   "sender@sender.test",
		RcptTo:     []string{"user+report@example.test"},
	}
	original := []byte("From: sender@sender.test\r\nTo: user@example.test\r\nSubject: test\r\n\r\nHello\r\n")

	tests := []struct {
		action      string
		contentType string
		want        []string
		wantAbsent  []string
	}{
		{action: "arf", contentType: "multipart/report", want: []string{"report-type=feedback-report", "Feedback-Type: abuse", "Content-Type: message/rfc822"}},
		{action: "xarf-v3", contentType: "multipart/report", want: []string{"Feedback-Type: xarf", "application/json; name=xarf.json"}},
		{action: "xarf-v4", contentType: "multipart/report", want: []string{"Feedback-Type: xarf", "application/json; name=xarf.json"}},
		{
			action:      "original-report",
			contentType: "multipart/mixed",
			want:        []string{"Content-Type: message/rfc822", "Content-Disposition: inline", "Hello"},
			wantAbsent:  []string{"report-type=feedback-report", "message/feedback-report", "Feedback-Type:"},
		},
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
			if test.action == "original-report" && !strings.Contains(message.Raw, "Subject: complaint about message from 192.0.2.55\r\n") {
				t.Fatal("original report must identify the SMTP source IP in its subject")
			}
			if test.action != "async-bounce" && test.action != "original-report" && !strings.Contains(message.Raw, "Subject: test\r\n") {
				t.Fatal("feedback report must preserve the original subject")
			}
			for _, want := range test.want {
				if !strings.Contains(message.Raw, want) {
					t.Fatalf("missing %q in report", want)
				}
			}
			for _, unwanted := range test.wantAbsent {
				if strings.Contains(message.Raw, unwanted) {
					t.Fatalf("unexpected %q in report", unwanted)
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
			}, SessionMetadata{RemoteIP: "192.0.2.55", RemotePort: 54321, MailFrom: "sender@sender.test"}, original, time.Now().UTC())
			if err != nil {
				t.Fatalf("build report: %v", err)
			}

			header, parts := parseMultipartReport(t, message.Raw)
			for _, required := range []string{"Date", "From", "To", "Subject", "MIME-Version", "Content-Type"} {
				if len(header.Values(required)) != 1 {
					t.Fatalf("outer report requires exactly one %s field, got %q", required, header.Values(required))
				}
			}
			if _, err := mail.ParseDate(header.Get("Date")); err != nil {
				t.Fatalf("invalid outer Date field: %v", err)
			}
			for _, addressField := range []string{"From", "To"} {
				if _, err := mail.ParseAddressList(header.Get(addressField)); err != nil {
					t.Fatalf("invalid outer %s field: %v", addressField, err)
				}
			}
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

			feedbackReader := textproto.NewReader(bufio.NewReader(strings.NewReader(parts[1].body)))
			feedback, err := feedbackReader.ReadMIMEHeader()
			if err != nil {
				t.Fatalf("parse message/feedback-report body as RFC fields: %v", err)
			}
			for field, want := range map[string]string{
				"Feedback-Type": test.wantFeedback,
				"User-Agent":    reportUserAgent,
				"Version":       "1",
			} {
				if values := feedback.Values(field); len(values) != 1 || values[0] != want {
					t.Fatalf("feedback report field %s is %q, want exactly %q", field, values, want)
				}
			}
			if strings.HasPrefix(test.action, "xarf-") {
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[2].body))
				if err != nil {
					t.Fatalf("decode XARF MIME attachment: %v", err)
				}
				var payload map[string]any
				if err := json.Unmarshal(decoded, &payload); err != nil {
					t.Fatalf("parse XARF MIME attachment: %v", err)
				}
			} else {
				if _, err := mail.ReadMessage(strings.NewReader(parts[2].body)); err != nil {
					t.Fatalf("parse attached original message: %v", err)
				}
			}
		})
	}
}

func TestOriginalReportMatchesMicrosoftMIMEShape(t *testing.T) {
	t.Parallel()

	original := "From: sender@sender.test\r\nTo: trap@example.test\r\nSubject: test\r\n\r\nbody\r\n"
	message, err := BuildReportMessage(ReportRequest{
		Action:    "original-report",
		Recipient: "trap@example.test",
		From:      "reports@example.test",
	}, SessionMetadata{
		RemoteIP: "192.0.2.55",
		MailFrom: "sender@sender.test",
	}, []byte(original), time.Now().UTC())
	if err != nil {
		t.Fatalf("build original report: %v", err)
	}

	header, parts := parseMultipartReport(t, message.Raw)
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse outer content type: %v", err)
	}
	if mediaType != "multipart/mixed" || params["report-type"] != "" {
		t.Fatalf("unexpected outer content type: %q", header.Get("Content-Type"))
	}
	if len(parts) != 1 {
		t.Fatalf("original report requires exactly one MIME part, got %d", len(parts))
	}
	partType, _, err := mime.ParseMediaType(parts[0].header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse original part content type: %v", err)
	}
	if partType != "message/rfc822" || parts[0].header.Get("Content-Disposition") != "inline" {
		t.Fatalf("unexpected original part headers: %#v", parts[0].header)
	}
	if parts[0].header.Get("Content-Transfer-Encoding") != "" {
		t.Fatalf("Microsoft-style original part must not add a transfer encoding: %#v", parts[0].header)
	}
	if parts[0].body != original {
		t.Fatalf("embedded message changed:\ngot:  %q\nwant: %q", parts[0].body, original)
	}
	if strings.Contains(message.Raw, "message/feedback-report") || strings.Contains(message.Raw, "Feedback-Type:") {
		t.Fatal("original report must not be identifiable as ARF")
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

	session := SessionMetadata{RemoteIP: "192.0.2.55", RemotePort: 54321, MailFrom: "sender@sender.test"}
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
		if version == 4 && value["xarf_version"] != "4.2.0" {
			t.Fatalf("unexpected v4 payload: %s", payload)
		}
	}
}

func TestGeneratedXARFPayloadsMatchOfficialSchemas(t *testing.T) {
	t.Parallel()

	session := SessionMetadata{
		RemoteIP:   "192.0.2.55",
		RemotePort: 54321,
		MailFrom:   "sender@sender.test",
	}
	request := ReportRequest{From: "reports@example.test", Recipient: "trap@example.test"}
	original := []byte("From: sender@sender.test\r\nTo: trap@example.test\r\nSubject: test\r\nMessage-ID: <sample@sender.test>\r\n\r\nbody\r\n")
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		version   int
		resources map[string]string
		schemaURL string
	}{
		{
			name:    "v3 spam",
			version: 3,
			resources: map[string]string{
				"https://raw.githubusercontent.com/xarf/schema-discussion/master/schemas/3/xarf_shared.schema.json": filepath.Join("testdata", "xarf", "v3", "xarf_shared.schema.json"),
				"https://raw.githubusercontent.com/xarf/schema-discussion/master/schemas/3/spam.schema.json":        filepath.Join("testdata", "xarf", "v3", "spam.schema.json"),
			},
			schemaURL: "https://raw.githubusercontent.com/xarf/schema-discussion/master/schemas/3/spam.schema.json",
		},
		{
			name:    "v4 messaging spam",
			version: 4,
			resources: map[string]string{
				"https://xarf.org/schemas/v4/xarf-core.json":            filepath.Join("testdata", "xarf", "v4", "xarf-core.json"),
				"https://xarf.org/schemas/v4/types/messaging-spam.json": filepath.Join("testdata", "xarf", "v4", "types", "messaging-spam.json"),
			},
			schemaURL: "https://xarf.org/schemas/v4/types/messaging-spam.json",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			compiler := jsonschema.NewCompiler()
			compiler.AssertFormat()
			for resourceURL, path := range test.resources {
				file, err := os.Open(path)
				if err != nil {
					t.Fatalf("open pinned schema: %v", err)
				}
				document, decodeErr := jsonschema.UnmarshalJSON(file)
				closeErr := file.Close()
				if decodeErr != nil {
					t.Fatalf("decode pinned schema: %v", decodeErr)
				}
				if closeErr != nil {
					t.Fatalf("close pinned schema: %v", closeErr)
				}
				if err := compiler.AddResource(resourceURL, document); err != nil {
					t.Fatalf("add pinned schema: %v", err)
				}
			}
			schema, err := compiler.Compile(test.schemaURL)
			if err != nil {
				t.Fatalf("compile pinned schema: %v", err)
			}
			payload, err := buildXARFPayload(request, session, original, now, test.version)
			if err != nil {
				t.Fatalf("build XARF: %v", err)
			}
			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(payload))
			if err != nil {
				t.Fatalf("decode generated XARF: %v", err)
			}
			if err := schema.Validate(instance); err != nil {
				t.Fatalf("generated XARF does not match official schema: %v\n%s", err, payload)
			}
		})
	}
}

func TestXARFRejectsMissingTransportEvidence(t *testing.T) {
	t.Parallel()

	request := ReportRequest{From: "reports@example.test", Recipient: "trap@example.test"}
	original := []byte("Subject: test\r\n\r\nbody\r\n")
	for _, session := range []SessionMetadata{
		{RemoteIP: "not-an-ip", RemotePort: 54321, MailFrom: "sender@sender.test"},
		{RemoteIP: "192.0.2.55", RemotePort: 0, MailFrom: "sender@sender.test"},
	} {
		if _, err := buildXARFPayload(request, session, original, time.Now().UTC(), 4); err == nil {
			t.Fatalf("expected invalid XARF transport metadata to fail: %#v", session)
		}
	}
}

func TestARFRejectsInvalidSourceIP(t *testing.T) {
	t.Parallel()

	_, err := BuildReportMessage(ReportRequest{
		Action:       "arf",
		Recipient:    "trap@example.test",
		From:         "reports@example.test",
		FeedbackType: "abuse",
	}, SessionMetadata{RemoteIP: "not-an-ip", MailFrom: "sender@sender.test"}, []byte("Subject: test\r\n\r\nbody\r\n"), time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "valid source IP") {
		t.Fatalf("expected invalid ARF source IP to fail, got %v", err)
	}
}

func TestXARFEmailAttachmentContainsJSON(t *testing.T) {
	t.Parallel()

	message, err := BuildReportMessage(ReportRequest{
		Action:    "xarf-v4",
		Recipient: "trap@example.test",
		From:      "reports@example.test",
		Message:   "Spam report",
	}, SessionMetadata{RemoteIP: "192.0.2.55", RemotePort: 54321, MailFrom: "sender@sender.test"}, []byte("Subject: test\r\n\r\nbody"), time.Now().UTC())
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

func TestAsyncBounceMatchesRFC3464FieldStructure(t *testing.T) {
	t.Parallel()

	message, err := BuildReportMessage(ReportRequest{
		Action:       "async-bounce",
		Recipient:    "quota@example.test",
		From:         "postmaster@example.test",
		Code:         552,
		EnhancedCode: "5.2.2",
		Message:      "Mailbox quota exceeded",
	}, SessionMetadata{MailFrom: "sender@sender.test"}, []byte("From: sender@sender.test\r\nSubject: quota\r\n\r\nbody\r\n"), time.Now().UTC())
	if err != nil {
		t.Fatalf("build async bounce: %v", err)
	}
	header, parts := parseMultipartReport(t, message.Raw)
	_, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || params["report-type"] != "delivery-status" {
		t.Fatalf("invalid DSN outer Content-Type: %q (%v)", header.Get("Content-Type"), err)
	}
	if len(parts) != 3 {
		t.Fatalf("DSN requires human, delivery-status, and original parts; got %d", len(parts))
	}
	statusReader := textproto.NewReader(bufio.NewReader(strings.NewReader(parts[1].body)))
	perMessage, err := statusReader.ReadMIMEHeader()
	if err != nil {
		t.Fatalf("parse DSN per-message fields: %v", err)
	}
	perRecipient, err := statusReader.ReadMIMEHeader()
	if err != nil {
		t.Fatalf("parse DSN per-recipient fields: %v", err)
	}
	if perMessage.Get("Reporting-MTA") != "dns; example.test" {
		t.Fatalf("invalid Reporting-MTA: %q", perMessage.Get("Reporting-MTA"))
	}
	for field, want := range map[string]string{
		"Final-Recipient": "rfc822; quota@example.test",
		"Action":          "failed",
		"Status":          "5.2.2",
		"Diagnostic-Code": "smtp; 552 5.2.2 Mailbox quota exceeded",
	} {
		if values := perRecipient.Values(field); len(values) != 1 || values[0] != want {
			t.Fatalf("DSN field %s is %q, want exactly %q", field, values, want)
		}
	}
}

func TestAsyncBounceRejectsNonASCIIDiagnostic(t *testing.T) {
	t.Parallel()

	_, err := NewMailFailEngine([]models.MailFailRule{{
		Name: "quota", Trigger: "quota", Action: "async-bounce", Message: "Postfach ist voll – erneut versuchen",
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "printable US-ASCII") {
		t.Fatalf("expected invalid DSN diagnostic to be rejected, got %v", err)
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

func TestFeedbackReportRecipientLocalPart(t *testing.T) {
	t.Parallel()

	for _, action := range []string{"arf", "xarf-v3", "xarf-v4", "original-report"} {
		action := action
		t.Run(action, func(t *testing.T) {
			t.Parallel()

			engine, err := NewMailFailEngine([]models.MailFailRule{{
				Name:                     action,
				Trigger:                  action,
				Action:                   action,
				ReportRecipientLocalPart: "fbl",
			}}, nil)
			if err != nil {
				t.Fatalf("create engine: %v", err)
			}
			requests := engine.MatchReports([]string{"user+" + action + "@mailtail.test"})
			if len(requests) != 1 || requests[0].ReportRecipientLocalPart != "fbl" {
				t.Fatalf("unexpected report request: %#v", requests)
			}
			requests[0].From = "postmaster@mailtail.test"

			message, err := BuildReportMessage(requests[0], SessionMetadata{
				MailFrom:   "bounce-123@sender.test",
				RemoteIP:   "192.0.2.55",
				RemotePort: 12345,
			}, []byte("From: sender@sender.test\r\nSubject: test\r\n\r\nbody\r\n"), time.Now().UTC())
			if err != nil {
				t.Fatalf("build report: %v", err)
			}
			if message.Recipient != "fbl@sender.test" {
				t.Fatalf("unexpected report recipient: %q", message.Recipient)
			}
			if !strings.Contains(message.Raw, "To: <fbl@sender.test>\r\n") {
				t.Fatalf("report To header does not use configured local part:\n%s", message.Raw)
			}
		})
	}
}

func TestFeedbackReportRecipientLocalPartValidation(t *testing.T) {
	t.Parallel()

	for _, localPart := range []string{"fbl@example.test", ".fbl", "fbl.", "fbl..reports", "fbl reports"} {
		localPart := localPart
		t.Run(localPart, func(t *testing.T) {
			t.Parallel()
			_, err := NewMailFailEngine([]models.MailFailRule{{
				Name:                     "arf",
				Trigger:                  "arf",
				Action:                   "arf",
				ReportRecipientLocalPart: localPart,
			}}, nil)
			if err == nil || !strings.Contains(err.Error(), "invalid report recipient local part") {
				t.Fatalf("expected invalid local part %q to fail, got %v", localPart, err)
			}
		})
	}

	_, err := NewMailFailEngine([]models.MailFailRule{{
		Name:                     "bounce",
		Trigger:                  "bounce",
		Action:                   "async-bounce",
		ReportRecipientLocalPart: "fbl",
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported action") {
		t.Fatalf("expected local part on async bounce to fail, got %v", err)
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
