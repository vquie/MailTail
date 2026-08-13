package smtpserver

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	"github.com/vquie/MailTail/internal/models"
)

const reportUserAgent = "MailTail"

func BuildReportMessage(request ReportRequest, session SessionMetadata, original []byte, now time.Time) (models.OutboundMessage, error) {
	from, err := mail.ParseAddress(strings.TrimSpace(request.From))
	if err != nil || from.Address != strings.TrimSpace(request.From) || !isASCII(from.Address) {
		return models.OutboundMessage{}, fmt.Errorf("invalid report sender")
	}
	if _, ok := extractDomain(from.Address); !ok {
		return models.OutboundMessage{}, fmt.Errorf("invalid report sender")
	}
	originalSender, err := mail.ParseAddress(strings.TrimSpace(session.MailFrom))
	if err != nil || originalSender.Address != strings.TrimSpace(session.MailFrom) || !isASCII(originalSender.Address) {
		return models.OutboundMessage{}, fmt.Errorf("invalid report recipient")
	}
	to, err := resolveReportRecipient(request, originalSender)
	if err != nil {
		return models.OutboundMessage{}, err
	}
	reportedRecipient, err := mail.ParseAddress(strings.TrimSpace(request.Recipient))
	if err != nil || reportedRecipient.Address != strings.TrimSpace(request.Recipient) || !isASCII(reportedRecipient.Address) {
		return models.OutboundMessage{}, fmt.Errorf("invalid reported recipient")
	}
	if _, err := mail.ReadMessage(bytes.NewReader(original)); err != nil {
		return models.OutboundMessage{}, fmt.Errorf("invalid original message: %w", err)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	mediaType := "multipart/report"
	reportType := "feedback-report"
	subject := reportSubject(original, "MailTail report")
	if request.Action == "original-report" {
		mediaType = "multipart/mixed"
		reportType = ""
		subject = originalReportSubject(session)
	} else if request.Action == "async-bounce" {
		reportType = "delivery-status"
		subject = "Delivery Status Notification (Failure)"
	}

	if request.Action != "original-report" {
		if err := writeHumanReadablePart(writer, request.Message); err != nil {
			return models.OutboundMessage{}, err
		}
	}
	switch request.Action {
	case "arf":
		err = writeARFParts(writer, request, session, original, now)
	case "xarf-v3":
		err = writeXARFParts(writer, request, session, original, now, 3)
	case "xarf-v4":
		err = writeXARFParts(writer, request, session, original, now, 4)
	case "original-report":
		err = writeInlineOriginalPart(writer, original)
	case "async-bounce":
		err = writeDSNParts(writer, request, session, original, now)
	default:
		err = fmt.Errorf("unsupported report action %q", request.Action)
	}
	if err != nil {
		return models.OutboundMessage{}, err
	}
	if err := writer.Close(); err != nil {
		return models.OutboundMessage{}, err
	}

	domain, _ := extractDomain(from.Address)
	messageID := newReportID()
	fromAddress := &mail.Address{Name: "MailTail Reports", Address: from.Address}
	fromHeader := fromAddress.String()
	if request.Action == "async-bounce" {
		fromAddress.Name = "Mail Delivery System"
		fromHeader = fromAddress.String()
	}
	contentType := fmt.Sprintf("%s; boundary=%q", mediaType, writer.Boundary())
	if reportType != "" {
		contentType = fmt.Sprintf("%s; report-type=%s; boundary=%q", mediaType, reportType, writer.Boundary())
	}

	var raw bytes.Buffer
	fmt.Fprintf(&raw, "From: %s\r\n", fromHeader)
	fmt.Fprintf(&raw, "To: %s\r\n", to.String())
	fmt.Fprintf(&raw, "Date: %s\r\n", now.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&raw, "Message-ID: <%s@%s>\r\n", messageID, domain)
	fmt.Fprintf(&raw, "Subject: %s\r\n", encodeHeader(subject))
	raw.WriteString("Auto-Submitted: auto-generated\r\n")
	raw.WriteString("X-Auto-Response-Suppress: All\r\n")
	raw.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&raw, "Content-Type: %s\r\n\r\n", contentType)
	raw.Write(body.Bytes())

	envelopeFrom := from.Address
	if request.Action == "async-bounce" {
		envelopeFrom = ""
	}
	return models.OutboundMessage{
		EnvelopeFrom: envelopeFrom,
		Recipient:    to.Address,
		Raw:          raw.String(),
		NextAttempt:  now.UTC(),
	}, nil
}

func resolveReportRecipient(request ReportRequest, originalSender *mail.Address) (*mail.Address, error) {
	localPart := strings.TrimSpace(request.ReportRecipientLocalPart)
	if localPart == "" {
		return originalSender, nil
	}
	if !supportsReportRecipientLocalPart(request.Action) {
		return nil, fmt.Errorf("report recipient local part is unsupported for action %q", request.Action)
	}
	if !validReportRecipientLocalPart(localPart) {
		return nil, fmt.Errorf("invalid report recipient local part")
	}
	domain, ok := extractDomain(originalSender.Address)
	if !ok {
		return nil, fmt.Errorf("invalid report recipient domain")
	}
	recipient := localPart + "@" + domain
	parsed, err := mail.ParseAddress(recipient)
	if err != nil || parsed.Address != recipient || !isASCII(parsed.Address) {
		return nil, fmt.Errorf("invalid report recipient")
	}
	return parsed, nil
}

func writeHumanReadablePart(writer *multipart.Writer, message string) error {
	if strings.TrimSpace(message) == "" {
		message = "MailTail generated this report for the accepted test message."
	}
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	encoded := quotedprintable.NewWriter(part)
	if _, err := fmt.Fprintf(encoded, "%s\r\n", normalizeText(message)); err != nil {
		_ = encoded.Close()
		return err
	}
	return encoded.Close()
}

func writeARFParts(writer *multipart.Writer, request ReportRequest, session SessionMetadata, original []byte, now time.Time) error {
	feedbackType := strings.ToLower(strings.TrimSpace(request.FeedbackType))
	if feedbackType == "" {
		feedbackType = "abuse"
	}
	switch feedbackType {
	case "abuse", "fraud", "virus", "other", "not-spam":
	default:
		return fmt.Errorf("unsupported ARF feedback type %q", request.FeedbackType)
	}
	if err := writeFeedbackReportPart(writer, feedbackType, request, session, now); err != nil {
		return err
	}
	return writeOriginalPart(writer, original)
}

func writeFeedbackReportPart(writer *multipart.Writer, feedbackType string, request ReportRequest, session SessionMetadata, now time.Time) error {
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"message/feedback-report"},
		"Content-Disposition":       {"inline"},
		"Content-Transfer-Encoding": {"7bit"},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(part, "Feedback-Type: %s\r\n", feedbackType)
	fmt.Fprintf(part, "User-Agent: %s\r\n", reportUserAgent)
	fmt.Fprintf(part, "Version: 1\r\n")
	fmt.Fprintf(part, "Original-Mail-From: <%s>\r\n", session.MailFrom)
	fmt.Fprintf(part, "Original-Rcpt-To: <%s>\r\n", request.Recipient)
	domain, _ := extractDomain(request.From)
	fmt.Fprintf(part, "Reporting-MTA: dns; %s\r\n", domain)
	if sourceIPText := strings.TrimSpace(session.RemoteIP); sourceIPText != "" {
		sourceIP := net.ParseIP(sourceIPText)
		if sourceIP == nil {
			return fmt.Errorf("feedback report requires a valid source IP")
		}
		fmt.Fprintf(part, "Source-IP: %s\r\n", sourceIP.String())
	}
	fmt.Fprintf(part, "Arrival-Date: %s\r\n", now.UTC().Format(time.RFC1123Z))
	_, err = fmt.Fprint(part, "\r\n")
	return err
}

func writeXARFParts(writer *multipart.Writer, request ReportRequest, session SessionMetadata, original []byte, now time.Time, version int) error {
	if err := writeFeedbackReportPart(writer, "xarf", request, session, now); err != nil {
		return err
	}

	payload, err := buildXARFPayload(request, session, original, now, version)
	if err != nil {
		return err
	}
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"application/json; name=xarf.json"},
		"Content-Disposition":       {"attachment; filename=xarf.json"},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	_, err = fmt.Fprint(part, wrapBase64(encoded))
	return err
}

func buildXARFPayload(request ReportRequest, session SessionMetadata, original []byte, now time.Time, version int) ([]byte, error) {
	domain, _ := extractDomain(request.From)
	sourceIP := net.ParseIP(strings.TrimSpace(session.RemoteIP))
	if sourceIP == nil {
		return nil, fmt.Errorf("XARF requires a valid source IP")
	}
	if session.RemotePort < 1 || session.RemotePort > 65535 {
		return nil, fmt.Errorf("XARF requires a valid SMTP source port")
	}
	if version == 3 {
		value := map[string]any{
			"Version": "3",
			"ReporterInfo": map[string]any{
				"ReporterOrg":       "MailTail",
				"ReporterOrgDomain": domain,
				"ReporterOrgEmail":  request.From,
			},
			"Disclosure": true,
			"Report": map[string]any{
				"ReportClass":         "Activity",
				"ReportType":          "Spam",
				"ReportSubType":       "Trap",
				"Date":                now.UTC().Format(time.RFC3339),
				"SourceIp":            sourceIP.String(),
				"SourcePort":          session.RemotePort,
				"DestinationPort":     25,
				"SmtpMailFromAddress": session.MailFrom,
				"SmtpRcptToAddress":   request.Recipient,
				"Samples": []any{map[string]any{
					"ContentType":   "message/rfc822",
					"Base64Encoded": true,
					"Description":   "Original test message",
					"Payload":       base64.StdEncoding.EncodeToString(original),
				}},
			},
		}
		return json.MarshalIndent(value, "", "  ")
	}
	if version != 4 {
		return nil, fmt.Errorf("unsupported XARF version %d", version)
	}

	digest := sha256.Sum256(original)
	contact := map[string]any{"org": "MailTail", "contact": request.From, "domain": domain}
	value := map[string]any{
		"xarf_version":      "4.2.0",
		"report_id":         newReportID(),
		"timestamp":         now.UTC().Format(time.RFC3339),
		"reporter":          contact,
		"sender":            contact,
		"source_identifier": sourceIP.String(),
		"source_port":       session.RemotePort,
		"category":          "messaging",
		"type":              "spam",
		"evidence_source":   "spamtrap",
		"protocol":          "smtp",
		"smtp_from":         session.MailFrom,
		"smtp_to":           request.Recipient,
		"evidence": []any{map[string]any{
			"content_type": "message/rfc822",
			"description":  "Original test message",
			"payload":      base64.StdEncoding.EncodeToString(original),
			"hash":         "sha256:" + hex.EncodeToString(digest[:]),
		}},
	}
	if message, err := mail.ReadMessage(bytes.NewReader(original)); err == nil {
		if subject := decodedHeader(message.Header.Get("Subject")); subject != "" {
			value["subject"] = subject
		}
		if messageID := strings.TrimSpace(message.Header.Get("Message-ID")); messageID != "" {
			value["message_id"] = messageID
		}
	}
	return json.MarshalIndent(value, "", "  ")
}

func writeDSNParts(writer *multipart.Writer, request ReportRequest, session SessionMetadata, original []byte, now time.Time) error {
	if request.Code < 500 || request.Code > 599 {
		return fmt.Errorf("async bounce requires a permanent SMTP status code")
	}
	if !enhancedBounceCodePattern.MatchString(strings.TrimSpace(request.EnhancedCode)) {
		return fmt.Errorf("async bounce requires a valid permanent enhanced status code")
	}
	if !validDSNDiagnostic(request.Message) {
		return fmt.Errorf("async bounce diagnostic must be printable US-ASCII and at most 700 characters")
	}
	domain, _ := extractDomain(request.From)
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"message/delivery-status"},
		"Content-Transfer-Encoding": {"7bit"},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(part, "Reporting-MTA: dns; %s\r\n", domain)
	fmt.Fprintf(part, "Arrival-Date: %s\r\n\r\n", now.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(part, "Final-Recipient: rfc822; %s\r\n", request.Recipient)
	fmt.Fprintf(part, "Action: failed\r\n")
	fmt.Fprintf(part, "Status: %s\r\n", request.EnhancedCode)
	diagnostic := strings.Join(strings.Fields(request.Message), " ")
	fmt.Fprintf(part, "Diagnostic-Code: smtp; %d %s %s\r\n", request.Code, request.EnhancedCode, diagnostic)
	if _, err := fmt.Fprint(part, "\r\n"); err != nil {
		return err
	}
	return writeOriginalPart(writer, original)
}

func writeOriginalPart(writer *multipart.Writer, original []byte) error {
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"message/rfc822"},
		"Content-Disposition":       {"attachment; filename=original.eml"},
		"Content-Transfer-Encoding": {"8bit"},
	})
	if err != nil {
		return err
	}
	_, err = part.Write(original)
	return err
}

func writeInlineOriginalPart(writer *multipart.Writer, original []byte) error {
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type":        {"message/rfc822"},
		"Content-Disposition": {"inline"},
	})
	if err != nil {
		return err
	}
	_, err = part.Write(original)
	return err
}

func originalReportSubject(session SessionMetadata) string {
	if sourceIP := net.ParseIP(strings.TrimSpace(session.RemoteIP)); sourceIP != nil {
		return "complaint about message from " + sourceIP.String()
	}
	return "complaint about original message"
}

func reportSubject(original []byte, fallback string) string {
	message, err := mail.ReadMessage(bytes.NewReader(original))
	if err != nil {
		return fallback
	}
	subject := decodedHeader(message.Header.Get("Subject"))
	if subject == "" {
		return fallback
	}
	return subject
}

func decodedHeader(value string) string {
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err != nil {
		decoded = value
	}
	return strings.Join(strings.Fields(decoded), " ")
}

func encodeHeader(value string) string {
	for _, character := range value {
		if character < 32 || character > 126 {
			return mime.QEncoding.Encode("utf-8", value)
		}
	}
	return value
}

func normalizeText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

func isASCII(value string) bool {
	for _, character := range value {
		if character > 127 {
			return false
		}
	}
	return true
}

func validDSNDiagnostic(value string) bool {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || len(value) > 700 {
		return false
	}
	for _, character := range value {
		if character < 32 || character > 126 {
			return false
		}
	}
	return true
}

func newReportID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(buffer)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func wrapBase64(value string) string {
	var builder strings.Builder
	for len(value) > 76 {
		builder.WriteString(value[:76])
		builder.WriteString("\r\n")
		value = value[76:]
	}
	builder.WriteString(value)
	builder.WriteString("\r\n")
	return builder.String()
}
