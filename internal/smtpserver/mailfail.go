package smtpserver

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

type MailFailConfig struct {
	Rules []MailFailRule `yaml:"rules"`
}

type MailFailRule struct {
	Name          string `yaml:"name"`
	Trigger       string `yaml:"trigger"`
	Stage         string `yaml:"stage"`
	Action        string `yaml:"action"`
	AllowAfter    int    `yaml:"allowAfter"`
	MinRetryAfter string `yaml:"minRetryAfter"`
	ResetAfter    string `yaml:"resetAfter"`
	Code          int    `yaml:"code"`
	EnhancedCode  string `yaml:"enhancedCode"`
	Message       string `yaml:"message"`
}

type MailFailEngine struct {
	rules          []mailFailRule
	greylistStates map[string]greylistState
	mu             sync.Mutex
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
	message       string
}

type greylistState struct {
	firstSeen time.Time
	lastSeen  time.Time
	attempts  int
}

func LoadMailFailEngine(path string) (*MailFailEngine, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config MailFailConfig
	if err := yaml.Unmarshal(content, &config); err != nil {
		return nil, err
	}

	return NewMailFailEngine(config)
}

func NewMailFailEngine(config MailFailConfig) (*MailFailEngine, error) {
	engine := &MailFailEngine{
		rules:          make([]mailFailRule, 0, len(config.Rules)),
		greylistStates: make(map[string]greylistState),
	}

	for _, rule := range config.Rules {
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

	e.mu.Lock()
	defer e.mu.Unlock()

	state, ok := e.greylistStates[key]
	if ok && rule.resetAfter > 0 && now.Sub(state.lastSeen) >= rule.resetAfter {
		ok = false
	}
	if !ok {
		state = greylistState{
			firstSeen: now,
			lastSeen:  now,
			attempts:  1,
		}
		e.greylistStates[key] = state
		return &ResponseError{
			Code:    rule.code,
			Message: rule.replyText(),
		}
	}

	state.attempts++
	state.lastSeen = now
	e.greylistStates[key] = state

	if state.attempts <= rule.allowAfter {
		return &ResponseError{
			Code:    rule.code,
			Message: rule.replyText(),
		}
	}

	if rule.minRetryAfter > 0 && now.Sub(state.firstSeen) < rule.minRetryAfter {
		return &ResponseError{
			Code:    rule.code,
			Message: rule.replyText(),
		}
	}

	return nil
}

func compileMailFailRule(rule MailFailRule) (mailFailRule, error) {
	compiled := mailFailRule{
		name:         strings.TrimSpace(rule.Name),
		trigger:      strings.ToLower(strings.TrimSpace(rule.Trigger)),
		stage:        strings.ToLower(strings.TrimSpace(rule.Stage)),
		action:       strings.ToLower(strings.TrimSpace(rule.Action)),
		allowAfter:   rule.AllowAfter,
		code:         rule.Code,
		enhancedCode: strings.TrimSpace(rule.EnhancedCode),
		message:      strings.TrimSpace(rule.Message),
	}

	if compiled.trigger == "" {
		return mailFailRule{}, fmt.Errorf("mailfail rule missing trigger")
	}
	if compiled.name == "" {
		compiled.name = compiled.trigger
	}
	switch compiled.stage {
	case "mailfrom", "rcpt", "data":
	default:
		return mailFailRule{}, fmt.Errorf("mailfail rule %q has unsupported stage %q", compiled.name, rule.Stage)
	}
	if compiled.action == "" {
		compiled.action = "reject"
	}
	switch compiled.action {
	case "reject":
	case "greylist":
	default:
		return mailFailRule{}, fmt.Errorf("mailfail rule %q has unsupported action %q", compiled.name, rule.Action)
	}
	if compiled.code < 400 || compiled.code > 599 {
		return mailFailRule{}, fmt.Errorf("mailfail rule %q has invalid code %d", compiled.name, rule.Code)
	}
	if compiled.message == "" {
		return mailFailRule{}, fmt.Errorf("mailfail rule %q is missing message", compiled.name)
	}
	if compiled.action == "greylist" {
		if compiled.stage != "rcpt" && compiled.stage != "data" {
			return mailFailRule{}, fmt.Errorf("mailfail rule %q uses action greylist but stage %q is unsupported", compiled.name, rule.Stage)
		}
		if compiled.code < 400 || compiled.code > 499 {
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

	return compiled, nil
}

func (r mailFailRule) replyText() string {
	if r.enhancedCode == "" {
		return r.message
	}
	return r.enhancedCode + " " + r.message
}

func (r mailFailRule) greylistKey(mailFrom, address string) string {
	return strings.Join([]string{
		r.stage,
		r.trigger,
		strings.ToLower(strings.TrimSpace(mailFrom)),
		strings.ToLower(strings.TrimSpace(address)),
	}, "|")
}

func parseOptionalDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return time.ParseDuration(raw)
}

func extractLocalPart(address string) (string, bool) {
	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" {
		return "", false
	}

	at := strings.LastIndex(address, "@")
	if at <= 0 {
		return "", false
	}

	return address[:at], true
}

func plusAddressSegments(localPart string) []string {
	parts := strings.Split(localPart, "+")
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

func containsExactSegment(segments []string, trigger string) bool {
	for _, segment := range segments {
		if segment == trigger {
			return true
		}
	}
	return false
}
