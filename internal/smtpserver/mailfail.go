package smtpserver

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/storage"
)

type MailFailEngine struct {
	rules []mailFailRule
	store storage.Store
}

type mailFailRule struct {
	name          string
	trigger       string
	stage         string
	action        string
	allowAfter    int
	minRetryAfter time.Duration
	resetAfter    time.Duration
	code          int
	enhancedCode  string
	feedbackType  string
	message       string
}

type ReportRequest struct {
	Action       string
	Recipient    string
	From         string
	Code         int
	EnhancedCode string
	FeedbackType string
	Message      string
}

var enhancedBounceCodePattern = regexp.MustCompile(`^5\.[0-9]{1,3}\.[0-9]{1,3}$`)

func NewMailFailEngine(rules []models.MailFailRule, store storage.Store) (*MailFailEngine, error) {
	engine := &MailFailEngine{
		rules: make([]mailFailRule, 0, len(rules)),
		store: store,
	}

	for _, rule := range rules {
		compiled, err := compileMailFailRule(rule)
		if err != nil {
			return nil, err
		}
		engine.rules = append(engine.rules, compiled)
	}

	return engine, nil
}

func (e *MailFailEngine) Empty() bool {
	return e == nil || len(e.rules) == 0
}

func (e *MailFailEngine) RuleCount() int {
	if e == nil {
		return 0
	}
	return len(e.rules)
}

func (e *MailFailEngine) MatchMailFrom(address string) *ResponseError {
	return e.match("mailfrom", "", address)
}

func (e *MailFailEngine) MatchRcpt(mailFrom, address string) *ResponseError {
	return e.match("rcpt", mailFrom, address)
}

func (e *MailFailEngine) MatchData(mailFrom string, recipients []string) *ResponseError {
	if e == nil {
		return nil
	}
	for _, recipient := range recipients {
		if response := e.match("data", mailFrom, recipient); response != nil {
			return response
		}
	}
	return nil
}

func (e *MailFailEngine) MatchReports(recipients []string) []ReportRequest {
	if e == nil {
		return nil
	}

	requests := make([]ReportRequest, 0)
	for _, recipient := range recipients {
		localPart, ok := extractLocalPart(recipient)
		if !ok {
			continue
		}
		segments := plusAddressSegments(localPart)
		for _, rule := range e.rules {
			if rule.stage != "data" || !isReportAction(rule.action) {
				continue
			}
			if containsExactSegment(segments, rule.trigger) {
				requests = append(requests, ReportRequest{
					Action:       rule.action,
					Recipient:    recipient,
					Code:         rule.code,
					EnhancedCode: rule.enhancedCode,
					FeedbackType: rule.feedbackType,
					Message:      rule.message,
				})
				break
			}
		}
	}
	return requests
}

func (e *MailFailEngine) match(stage, mailFrom, address string) *ResponseError {
	if e == nil {
		return nil
	}

	localPart, ok := extractLocalPart(address)
	if !ok {
		return nil
	}

	segments := plusAddressSegments(localPart)
	for _, rule := range e.rules {
		if rule.stage != stage {
			continue
		}
		if containsExactSegment(segments, rule.trigger) {
			if isReportAction(rule.action) {
				continue
			}
			if rule.action == "greylist" {
				return e.greylistResponse(rule, mailFrom, address)
			}
			return &ResponseError{
				Code:    rule.code,
				Message: rule.replyText(),
			}
		}
	}

	return nil
}

func (e *MailFailEngine) greylistResponse(rule mailFailRule, mailFrom, address string) *ResponseError {
	key := rule.greylistKey(mailFrom, address)
	now := time.Now().UTC()
	ctx := context.Background()

	if e.store != nil && rule.resetAfter > 0 {
		_ = e.store.DeleteExpiredGreylistStates(ctx, now.Add(-rule.resetAfter))
	}

	state, ok, err := e.loadGreylistState(ctx, key)
	if !ok {
		if err != nil {
			return &ResponseError{Code: 451, Message: "Temporary server error"}
		}
		state = models.GreylistState{
			Key:       key,
			FirstSeen: now,
			LastSeen:  now,
			Attempts:  1,
		}
		if err := e.saveGreylistState(ctx, state); err != nil {
			return &ResponseError{Code: 451, Message: "Temporary server error"}
		}
		return &ResponseError{Code: rule.code, Message: rule.replyText()}
	}

	if rule.resetAfter > 0 && now.Sub(state.LastSeen) >= rule.resetAfter {
		state = models.GreylistState{
			Key:       key,
			FirstSeen: now,
			LastSeen:  now,
			Attempts:  1,
		}
		if err := e.saveGreylistState(ctx, state); err != nil {
			return &ResponseError{Code: 451, Message: "Temporary server error"}
		}
		return &ResponseError{Code: rule.code, Message: rule.replyText()}
	}

	state.Attempts++
	state.LastSeen = now
	if err := e.saveGreylistState(ctx, state); err != nil {
		return &ResponseError{Code: 451, Message: "Temporary server error"}
	}

	if state.Attempts <= rule.allowAfter {
		return &ResponseError{Code: rule.code, Message: rule.replyText()}
	}

	if rule.minRetryAfter > 0 && now.Sub(state.FirstSeen) < rule.minRetryAfter {
		return &ResponseError{Code: rule.code, Message: rule.replyText()}
	}

	return nil
}

func (e *MailFailEngine) loadGreylistState(ctx context.Context, key string) (models.GreylistState, bool, error) {
	if e.store == nil {
		return models.GreylistState{}, false, nil
	}
	return e.store.GetGreylistState(ctx, key)
}

func (e *MailFailEngine) saveGreylistState(ctx context.Context, state models.GreylistState) error {
	if e.store == nil {
		return nil
	}
	return e.store.SaveGreylistState(ctx, state)
}

func compileMailFailRule(rule models.MailFailRule) (mailFailRule, error) {
	compiled := mailFailRule{
		name:         strings.TrimSpace(rule.Name),
		trigger:      strings.ToLower(strings.TrimSpace(rule.Trigger)),
		stage:        strings.ToLower(strings.TrimSpace(rule.Stage)),
		action:       strings.ToLower(strings.TrimSpace(rule.Action)),
		allowAfter:   rule.AllowAfter,
		code:         rule.Code,
		enhancedCode: strings.TrimSpace(rule.EnhancedCode),
		feedbackType: strings.ToLower(strings.TrimSpace(rule.FeedbackType)),
		message:      strings.TrimSpace(rule.Message),
	}

	if compiled.trigger == "" {
		return mailFailRule{}, fmt.Errorf("mailfail rule missing trigger")
	}
	if compiled.name == "" {
		compiled.name = compiled.trigger
	}
	if compiled.action == "" {
		compiled.action = "reject"
	}
	switch compiled.action {
	case "reject", "greylist", "arf", "xarf-v3", "xarf-v4", "original-report", "async-bounce":
	default:
		return mailFailRule{}, fmt.Errorf("mailfail rule %q has unsupported action %q", compiled.name, rule.Action)
	}
	if isReportAction(compiled.action) {
		compiled.stage = "data"
		if compiled.message == "" {
			compiled.message = defaultReportMessage(compiled.action)
		}
	} else {
		switch compiled.stage {
		case "mailfrom", "rcpt", "data":
		default:
			return mailFailRule{}, fmt.Errorf("mailfail rule %q has unsupported stage %q", compiled.name, rule.Stage)
		}
		if compiled.message == "" {
			return mailFailRule{}, fmt.Errorf("mailfail rule %q is missing message", compiled.name)
		}
	}
	if compiled.action == "arf" {
		if compiled.feedbackType == "" {
			compiled.feedbackType = "abuse"
		}
		switch compiled.feedbackType {
		case "abuse", "fraud", "virus", "other", "not-spam":
		default:
			return mailFailRule{}, fmt.Errorf("mailfail rule %q uses unsupported ARF feedback type %q", compiled.name, rule.FeedbackType)
		}
	} else {
		compiled.feedbackType = ""
	}
	if (compiled.action == "reject" || compiled.action == "greylist") && (compiled.code < 400 || compiled.code > 599) {
		return mailFailRule{}, fmt.Errorf("mailfail rule %q has invalid code %d", compiled.name, rule.Code)
	}
	if compiled.action == "greylist" {
		if compiled.stage != "rcpt" && compiled.stage != "data" {
			return mailFailRule{}, fmt.Errorf("mailfail rule %q uses action greylist but stage %q is unsupported", compiled.name, rule.Stage)
		}
		if compiled.code < 400 || compiled.code >= 500 {
			return mailFailRule{}, fmt.Errorf("mailfail rule %q uses action greylist but code %d is not temporary", compiled.name, rule.Code)
		}
		if compiled.allowAfter <= 0 {
			compiled.allowAfter = 1
		}
		minRetryAfter, err := parseOptionalDuration(rule.MinRetryAfter)
		if err != nil {
			return mailFailRule{}, fmt.Errorf("mailfail rule %q has invalid minRetryAfter %q: %w", compiled.name, rule.MinRetryAfter, err)
		}
		compiled.minRetryAfter = minRetryAfter
		resetAfter, err := parseOptionalDuration(rule.ResetAfter)
		if err != nil {
			return mailFailRule{}, fmt.Errorf("mailfail rule %q has invalid resetAfter %q: %w", compiled.name, rule.ResetAfter, err)
		}
		if resetAfter <= 0 {
			resetAfter = time.Hour
		}
		compiled.resetAfter = resetAfter
	}
	if isReportAction(compiled.action) {
		if compiled.action == "async-bounce" {
			if !validDSNDiagnostic(compiled.message) {
				return mailFailRule{}, fmt.Errorf("mailfail rule %q uses async-bounce but message must be printable US-ASCII and at most 700 characters", compiled.name)
			}
			if compiled.code == 0 {
				compiled.code = 550
			}
			if compiled.code < 500 || compiled.code > 599 {
				return mailFailRule{}, fmt.Errorf("mailfail rule %q uses async-bounce but code %d is not permanent", compiled.name, compiled.code)
			}
			if compiled.enhancedCode == "" {
				compiled.enhancedCode = "5.0.0"
			}
			if !enhancedBounceCodePattern.MatchString(compiled.enhancedCode) {
				return mailFailRule{}, fmt.Errorf("mailfail rule %q uses async-bounce but enhanced code %q is invalid", compiled.name, compiled.enhancedCode)
			}
		}
	}

	return compiled, nil
}

func defaultReportMessage(action string) string {
	switch action {
	case "arf":
		return "MailTail generated an abuse feedback report for the accepted message."
	case "xarf-v3":
		return "MailTail generated a XARF v3 report for the accepted message."
	case "xarf-v4":
		return "MailTail generated a XARF v4 report for the accepted message."
	case "original-report":
		return "MailTail attached the original accepted message."
	case "async-bounce":
		return "Delivery failed after the message was accepted by MailTail."
	default:
		return "MailTail generated a report for the accepted message."
	}
}

func isReportAction(action string) bool {
	switch action {
	case "arf", "xarf-v3", "xarf-v4", "original-report", "async-bounce":
		return true
	default:
		return false
	}
}

func (r mailFailRule) replyText() string {
	if r.enhancedCode == "" {
		return r.message
	}
	return r.enhancedCode + " " + r.message
}

func (r mailFailRule) greylistKey(mailFrom, address string) string {
	return strings.ToLower(strings.Join([]string{r.trigger, r.stage, strings.TrimSpace(mailFrom), strings.TrimSpace(address)}, "|"))
}

func parseOptionalDuration(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	return time.ParseDuration(value)
}

func extractLocalPart(address string) (string, bool) {
	address = strings.TrimSpace(address)
	at := strings.Index(address, "@")
	if at <= 0 {
		return "", false
	}
	return strings.ToLower(address[:at]), true
}

func plusAddressSegments(localPart string) []string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(localPart)), "+")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		segments = append(segments, part)
	}
	return segments
}

func containsExactSegment(segments []string, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	for _, segment := range segments {
		if segment == needle {
			return true
		}
	}
	return false
}
