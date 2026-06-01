package smtpserver

import (
	"fmt"
	"net"
	"regexp"
	"strings"
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
	acceptedRcptDomains *addressMatcher
	acceptedFromDomains *addressMatcher
	allowedRemoteNets   []*net.IPNet
	mailFail            *MailFailEngine
}

func NewDomainPolicy(acceptedRcptDomains, acceptedFromDomains, allowedRemoteCIDRs []string, mailFail *MailFailEngine) (*DomainPolicy, error) {
	allowedRemoteNets, err := parseAllowedRemoteCIDRs(allowedRemoteCIDRs)
	if err != nil {
		return nil, err
	}

	rcptMatcher, err := newAddressMatcher(acceptedRcptDomains)
	if err != nil {
		return nil, fmt.Errorf("MAILTAIL_ACCEPTED_RCPT_DOMAINS: %w", err)
	}

	fromMatcher, err := newAddressMatcher(acceptedFromDomains)
	if err != nil {
		return nil, fmt.Errorf("MAILTAIL_ACCEPTED_FROM_DOMAINS: %w", err)
	}

	return &DomainPolicy{
		acceptedRcptDomains: rcptMatcher,
		acceptedFromDomains: fromMatcher,
		allowedRemoteNets:   allowedRemoteNets,
		mailFail:            mailFail,
	}, nil
}

func (p *DomainPolicy) OnConnect(session SessionMetadata) *ResponseError {
	if len(p.allowedRemoteNets) == 0 {
		return nil
	}

	ip := net.ParseIP(strings.TrimSpace(session.RemoteIP))
	if ip == nil {
		return &ResponseError{
			Code:    554,
			Message: "Connection not allowed",
		}
	}

	for _, network := range p.allowedRemoteNets {
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
	if response := p.mailFail.MatchMailFrom(from); response != nil {
		return response
	}
	if p.acceptedFromDomains.Empty() {
		return nil
	}
	if !p.acceptedFromDomains.Match(from) {
		return &ResponseError{
			Code:    550,
			Message: "Sender domain not allowed",
		}
	}
	return nil
}

func (p *DomainPolicy) OnRcptTo(_ SessionMetadata, recipient string) *ResponseError {
	if response := p.mailFail.MatchRcpt(recipient); response != nil {
		return response
	}
	if p.acceptedRcptDomains.Empty() {
		return nil
	}
	if !p.acceptedRcptDomains.Match(recipient) {
		return &ResponseError{
			Code:    550,
			Message: "Recipient domain not allowed",
		}
	}
	return nil
}

func (p *DomainPolicy) OnData(session SessionMetadata) *ResponseError {
	if response := p.mailFail.MatchData(session.RcptTo); response != nil {
		return response
	}
	return nil
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
