package smtpserver

import (
	"fmt"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

type MailFailConfig struct {
	Rules []MailFailRule `yaml:"rules"`
}

type MailFailRule struct {
	Name         string `yaml:"name"`
	Trigger      string `yaml:"trigger"`
	Stage        string `yaml:"stage"`
	Action       string `yaml:"action"`
	Code         int    `yaml:"code"`
	EnhancedCode string `yaml:"enhancedCode"`
	Message      string `yaml:"message"`
}

type MailFailEngine struct {
	rules []mailFailRule
}

type mailFailRule struct {
	name         string
	trigger      string
	stage        string
	action       string
	code         int
	enhancedCode string
	message      string
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
		rules: make([]mailFailRule, 0, len(config.Rules)),
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
	return e.match("mailfrom", address)
}

func (e *MailFailEngine) MatchRcpt(address string) *ResponseError {
	return e.match("rcpt", address)
}

func (e *MailFailEngine) MatchData(recipients []string) *ResponseError {
	if e == nil {
		return nil
	}
	for _, recipient := range recipients {
		if response := e.match("data", recipient); response != nil {
			return response
		}
	}
	return nil
}

func (e *MailFailEngine) match(stage, address string) *ResponseError {
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
			return &ResponseError{
				Code:    rule.code,
				Message: rule.replyText(),
			}
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
	if compiled.action != "reject" {
		return mailFailRule{}, fmt.Errorf("mailfail rule %q has unsupported action %q", compiled.name, rule.Action)
	}
	if compiled.code < 400 || compiled.code > 599 {
		return mailFailRule{}, fmt.Errorf("mailfail rule %q has invalid code %d", compiled.name, rule.Code)
	}
	if compiled.message == "" {
		return mailFailRule{}, fmt.Errorf("mailfail rule %q is missing message", compiled.name)
	}

	return compiled, nil
}

func (r mailFailRule) replyText() string {
	if r.enhancedCode == "" {
		return r.message
	}
	return r.enhancedCode + " " + r.message
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
