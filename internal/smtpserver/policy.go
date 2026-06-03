package smtpserver

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/storage"
)

type SMTPResponsePolicy interface {
	OnConnect(session SessionMetadata) *ResponseError
	OnMailFrom(session SessionMetadata, from string) *ResponseError
	OnRcptTo(session SessionMetadata, recipient string) *ResponseError
	OnData(session SessionMetadata) *ResponseError
}

type SessionMetadata struct {
	Helo     string
	RemoteIP string
	MailFrom string
	RcptTo   []string
}

type ResponseError struct {
	Code    int
	Message string
}

func (e *ResponseError) Error() string {
	return e.Message
}

type DomainPolicy struct {
	mu    sync.RWMutex
	state domainPolicyState
	store storage.Store
}

type DomainPolicyConfig struct {
	AcceptedRcptDomains []string
	AcceptedFromDomains []string
	AllowedRemoteCIDRs  []string
	MailFailEnabled     bool
	MailFailRulesFile   string
}

type domainPolicyState struct {
	acceptedRcptDomains *addressMatcher
	acceptedFromDomains *addressMatcher
	allowedRemoteNets   []*net.IPNet
	mailFail            *MailFailEngine
}

func DomainPolicyConfigFromSettings(settings models.AppSettings) DomainPolicyConfig {
	return DomainPolicyConfig{
		AcceptedRcptDomains: csvList(settings.AcceptedRcptDomains),
		AcceptedFromDomains: csvList(settings.AcceptedFromDomains),
		AllowedRemoteCIDRs:  csvList(settings.AllowedRemoteIPs),
		MailFailEnabled:     settings.MailFailEnabled,
		MailFailRulesFile:   strings.TrimSpace(settings.MailFailRulesFile),
	}
}

func NewDomainPolicy(config DomainPolicyConfig, store storage.Store) (*DomainPolicy, error) {
	state, err := buildDomainPolicyState(config, store)
	if err != nil {
		return nil, err
	}

	return &DomainPolicy{state: state, store: store}, nil
}

func (p *DomainPolicy) ApplyConfig(config DomainPolicyConfig) error {
	state, err := buildDomainPolicyState(config, p.store)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
	return nil
}

func buildDomainPolicyState(config DomainPolicyConfig, store storage.Store) (domainPolicyState, error) {
	allowedRemoteNets, err := parseAllowedRemoteCIDRs(config.AllowedRemoteCIDRs)
	if err != nil {
		return domainPolicyState{}, err
	}

	rcptMatcher, err := newAddressMatcher(config.AcceptedRcptDomains)
	if err != nil {
		return domainPolicyState{}, fmt.Errorf("MAILTAIL_ACCEPTED_RCPT_DOMAINS: %w", err)
	}

	fromMatcher, err := newAddressMatcher(config.AcceptedFromDomains)
	if err != nil {
		return domainPolicyState{}, fmt.Errorf("MAILTAIL_ACCEPTED_FROM_DOMAINS: %w", err)
	}

	var mailFail *MailFailEngine
	switch {
	case !config.MailFailEnabled:
	case strings.TrimSpace(config.MailFailRulesFile) == "":
		return domainPolicyState{}, fmt.Errorf("MAILTAIL_MAILFAIL_ENABLED=true requires MAILTAIL_MAILFAIL_RULES_FILE")
	default:
		engine, err := LoadMailFailEngine(strings.TrimSpace(config.MailFailRulesFile), store)
		if err != nil {
			return domainPolicyState{}, fmt.Errorf("load mailfail rules: %w", err)
		}
		mailFail = engine
	}

	return domainPolicyState{
		acceptedRcptDomains: rcptMatcher,
		acceptedFromDomains: fromMatcher,
		allowedRemoteNets:   allowedRemoteNets,
		mailFail:            mailFail,
	}, nil
}

func (p *DomainPolicy) OnConnect(session SessionMetadata) *ResponseError {
	state := p.snapshot()
	if len(state.allowedRemoteNets) == 0 {
		return nil
	}

	ip := net.ParseIP(strings.TrimSpace(session.RemoteIP))
	if ip == nil {
		return &ResponseError{
			Code:    554,
			Message: "Connection not allowed",
		}
	}

	for _, network := range state.allowedRemoteNets {
		if network.Contains(ip) {
			return nil
		}
	}

	return &ResponseError{
		Code:    554,
		Message: "Connection not allowed",
	}
}

func (p *DomainPolicy) OnMailFrom(_ SessionMetadata, from string) *ResponseError {
	state := p.snapshot()
	if response := state.mailFail.MatchMailFrom(from); response != nil {
		return response
	}
	if state.acceptedFromDomains.Empty() {
		return nil
	}
	if !state.acceptedFromDomains.Match(from) {
		return &ResponseError{
			Code:    550,
			Message: "Sender domain not allowed",
		}
	}
	return nil
}

func (p *DomainPolicy) OnRcptTo(session SessionMetadata, recipient string) *ResponseError {
	state := p.snapshot()
	if response := state.mailFail.MatchRcpt(session.MailFrom, recipient); response != nil {
		return response
	}
	if state.acceptedRcptDomains.Empty() {
		return nil
	}
	if !state.acceptedRcptDomains.Match(recipient) {
		return &ResponseError{
			Code:    550,
			Message: "Recipient domain not allowed",
		}
	}
	return nil
}

func (p *DomainPolicy) OnData(session SessionMetadata) *ResponseError {
	state := p.snapshot()
	if response := state.mailFail.MatchData(session.MailFrom, session.RcptTo); response != nil {
		return response
	}
	return nil
}

func (p *DomainPolicy) snapshot() domainPolicyState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

type addressMatcher struct {
	exactDomains map[string]struct{}
	patterns     []*regexp.Regexp
}

func newAddressMatcher(values []string) (*addressMatcher, error) {
	matcher := &addressMatcher{
		exactDomains: make(map[string]struct{}, len(values)),
		patterns:     make([]*regexp.Regexp, 0, len(values)),
	}

	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}

		lower := strings.ToLower(normalized)
		if isPlainDomain(lower) {
			matcher.exactDomains[lower] = struct{}{}
			continue
		}

		re, err := regexp.Compile(lower)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", value, err)
		}
		matcher.patterns = append(matcher.patterns, re)
	}

	return matcher, nil
}

func (m *addressMatcher) Empty() bool {
	return len(m.exactDomains) == 0 && len(m.patterns) == 0
}

func (m *addressMatcher) Match(address string) bool {
	normalizedAddress := strings.ToLower(strings.TrimSpace(address))
	if normalizedAddress == "" {
		return false
	}

	domain, ok := extractDomain(normalizedAddress)
	if ok {
		if _, allowed := m.exactDomains[domain]; allowed {
			return true
		}
	}

	for _, pattern := range m.patterns {
		if pattern.MatchString(normalizedAddress) {
			return true
		}
		if ok && pattern.MatchString(domain) {
			return true
		}
	}

	return false
}

func isPlainDomain(value string) bool {
	if value == "" || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}

	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '-':
		default:
			return false
		}
	}

	return strings.Contains(value, ".")
}

func extractDomain(address string) (string, bool) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", false
	}
	at := strings.LastIndex(address, "@")
	if at <= 0 || at == len(address)-1 {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(address[at+1:])), true
}

func parseAllowedRemoteCIDRs(values []string) ([]*net.IPNet, error) {
	networks := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if ip := net.ParseIP(value); ip != nil {
			maskBits := 32
			if ip.To4() == nil {
				maskBits = 128
			}
			networks = append(networks, &net.IPNet{
				IP:   ip,
				Mask: net.CIDRMask(maskBits, maskBits),
			})
			continue
		}

		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, err
		}
		networks = append(networks, network)
	}
	return networks, nil
}

func csvList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		values = append(values, part)
	}
	return values
}
