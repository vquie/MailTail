package smtpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"sync"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/storage"
)

type SMTPResponsePolicy interface {
	OnConnect(session *SessionMetadata) *ResponseError
	OnMailFrom(session *SessionMetadata, from string) *ResponseError
	OnRcptTo(session *SessionMetadata, recipient string) *ResponseError
	OnData(session *SessionMetadata) *ResponseError
}

type MessageReportPolicy interface {
	ReportsFor(session *SessionMetadata) ([]ReportRequest, error)
}

type SessionMetadata struct {
	Helo        string
	RemoteIP    string
	MailFrom    string
	RcptTo      []string
	OwnerUserID int64
}

type ResponseError struct {
	Code    int
	Message string
}

func (e *ResponseError) Error() string {
	return e.Message
}

type DomainPolicy struct {
	mu      sync.RWMutex
	state   domainPolicyState
	store   storage.Store
	engines map[string]*MailFailEngine
}

type DomainPolicyConfig struct {
	AcceptedRcptDomains []string
	AcceptedFromDomains []string
	AllowedRemoteCIDRs  []string
	MailFailEnabled     bool
	MailFailRules       []models.MailFailRule
	ReportFrom          string
}

var ErrRecipientOwnershipConflict = errors.New("recipient ownership conflicts with an existing mailbox policy")

type domainPolicyState struct {
	userID              int64
	acceptedRcptDomains *addressMatcher
	acceptedFromDomains *addressMatcher
	allowedRemoteNets   []*net.IPNet
	mailFail            *MailFailEngine
	reportFrom          string
}

func DomainPolicyConfigFromSettings(settings models.AppSettings) DomainPolicyConfig {
	return DomainPolicyConfig{
		AcceptedRcptDomains: csvList(settings.AcceptedRcptDomains),
		AcceptedFromDomains: csvList(settings.AcceptedFromDomains),
		AllowedRemoteCIDRs:  csvList(settings.AllowedRemoteIPs),
		MailFailEnabled:     settings.MailFailEnabled,
		MailFailRules:       settings.MailFailRules,
		ReportFrom:          settings.ReportFrom,
	}
}

func NewDomainPolicy(config DomainPolicyConfig, store storage.Store) (*DomainPolicy, error) {
	state, err := buildDomainPolicyState(0, config, store)
	if err != nil {
		return nil, err
	}

	return &DomainPolicy{state: state, store: store, engines: make(map[string]*MailFailEngine)}, nil
}

func (p *DomainPolicy) ApplyConfig(config DomainPolicyConfig) error {
	state, err := buildDomainPolicyState(0, config, p.store)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
	return nil
}

func BuildUserPolicyState(userID int64, settings models.AppSettings, store storage.Store) (domainPolicyState, error) {
	return buildDomainPolicyState(userID, DomainPolicyConfigFromSettings(settings), store)
}

func ValidateRecipientOwnership(ctx context.Context, ownerUserID int64, settings models.AppSettings, store storage.Store) error {
	if store == nil {
		return nil
	}

	candidate, err := BuildUserPolicyState(ownerUserID, settings, store)
	if err != nil {
		return err
	}

	if candidate.acceptedRcptDomains.Empty() {
		return nil
	}

	adminSettings, ok, err := store.LoadAdminMailboxSettings(ctx)
	if err != nil {
		return err
	}
	if ok && ownerUserID != 0 {
		adminState, err := BuildUserPolicyState(0, adminSettings, store)
		if err != nil {
			return err
		}
		if recipientOwnershipOverlaps(candidate, adminState) {
			return ErrRecipientOwnershipConflict
		}
	}

	users, err := store.ListUsers(ctx)
	if err != nil {
		return err
	}
	for _, user := range users {
		if user.ID == ownerUserID {
			continue
		}
		other, err := BuildUserPolicyState(user.ID, user.Settings, store)
		if err != nil {
			return err
		}
		if other.acceptedRcptDomains.Empty() {
			continue
		}
		if recipientOwnershipOverlaps(candidate, other) {
			return ErrRecipientOwnershipConflict
		}
	}

	return nil
}

func buildDomainPolicyState(userID int64, config DomainPolicyConfig, store storage.Store) (domainPolicyState, error) {
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
	case len(config.MailFailRules) == 0:
		return domainPolicyState{}, fmt.Errorf("MAILTAIL_MAILFAIL_ENABLED=true requires at least one MailFail rule")
	default:
		engine, err := NewMailFailEngine(config.MailFailRules, store)
		if err != nil {
			return domainPolicyState{}, fmt.Errorf("invalid mailfail rules: %w", err)
		}
		mailFail = engine
	}

	reportFrom := strings.TrimSpace(config.ReportFrom)
	if reportFrom != "" {
		address, err := mail.ParseAddress(reportFrom)
		_, hasDomain := extractDomain(reportFrom)
		if err != nil || !strings.EqualFold(address.Address, reportFrom) || !hasDomain {
			return domainPolicyState{}, fmt.Errorf("reportFrom must be a single email address")
		}
		reportFrom = address.Address
	}
	if config.MailFailEnabled && hasReportRules(config.MailFailRules) && reportFrom == "" && len(rcptMatcher.exactDomains) == 0 {
		return domainPolicyState{}, fmt.Errorf("reportFrom is required for report rules unless an exact accepted recipient domain is configured")
	}

	return domainPolicyState{
		userID:              userID,
		acceptedRcptDomains: rcptMatcher,
		acceptedFromDomains: fromMatcher,
		allowedRemoteNets:   allowedRemoteNets,
		mailFail:            mailFail,
		reportFrom:          reportFrom,
	}, nil
}

func (p *DomainPolicy) ReportsFor(session *SessionMetadata) ([]ReportRequest, error) {
	if session == nil || strings.TrimSpace(session.MailFrom) == "" {
		return nil, nil
	}

	state, err := p.reportState(session.OwnerUserID)
	if err != nil || state.mailFail == nil {
		return nil, err
	}
	requests := state.mailFail.MatchReports(session.RcptTo)
	for index := range requests {
		requests[index].From, err = state.resolveReportFrom(requests[index].Recipient)
		if err != nil {
			return nil, err
		}
	}
	return requests, nil
}

func (p *DomainPolicy) reportState(ownerUserID int64) (domainPolicyState, error) {
	if ownerUserID == 0 {
		users, err := p.policyUsers()
		if err != nil {
			return domainPolicyState{}, err
		}
		for _, state := range users {
			if state.userID == 0 {
				return state, nil
			}
		}
		return p.snapshot(), nil
	}
	users, err := p.policyUsers()
	if err != nil {
		return domainPolicyState{}, err
	}
	for _, state := range users {
		if state.userID == ownerUserID {
			return state, nil
		}
	}
	return domainPolicyState{}, fmt.Errorf("mailbox policy not found")
}

func (s domainPolicyState) resolveReportFrom(recipient string) (string, error) {
	if s.reportFrom != "" {
		return s.reportFrom, nil
	}
	domain, ok := extractDomain(recipient)
	if !ok {
		return "", fmt.Errorf("cannot derive report sender from recipient %q", recipient)
	}
	if _, allowed := s.acceptedRcptDomains.exactDomains[domain]; !allowed {
		return "", fmt.Errorf("cannot derive report sender from non-exact recipient domain %q", domain)
	}
	return "postmaster@" + domain, nil
}

func hasReportRules(rules []models.MailFailRule) bool {
	for _, rule := range rules {
		if isReportAction(strings.ToLower(strings.TrimSpace(rule.Action))) {
			return true
		}
	}
	return false
}

func (p *DomainPolicy) OnConnect(session *SessionMetadata) *ResponseError {
	if session == nil {
		return nil
	}
	users, err := p.policyUsers()
	if err != nil {
		return &ResponseError{Code: 451, Message: "Temporary policy lookup failure"}
	}
	if len(users) > 0 {
		return nil
	}
	return connectAllowed(p.snapshot(), *session)
}

func (p *DomainPolicy) OnMailFrom(session *SessionMetadata, from string) *ResponseError {
	if session == nil {
		return nil
	}
	session.MailFrom = from
	return nil
}

func (p *DomainPolicy) OnRcptTo(session *SessionMetadata, recipient string) *ResponseError {
	if session == nil {
		return nil
	}

	users, err := p.policyUsers()
	if err != nil {
		return &ResponseError{Code: 451, Message: "Temporary policy lookup failure"}
	}
	domainScoped := hasRecipientDomainRestrictions(users)

	candidates := make([]domainPolicyState, 0)
	for _, user := range users {
		if domainScoped && user.acceptedRcptDomains.Empty() {
			continue
		}
		if matchesUserPolicy(user, *session, recipient) {
			candidates = append(candidates, user)
		}
	}

	switch len(candidates) {
	case 0:
		if len(users) > 0 {
			return &ResponseError{Code: 550, Message: "Recipient not allowed"}
		}
		state := p.snapshot()
		if response := defaultRcptResponse(state, *session, recipient); response != nil {
			return response
		}
		session.OwnerUserID = 0
		return nil
	case 1:
		if session.OwnerUserID != 0 && session.OwnerUserID != candidates[0].userID {
			return &ResponseError{Code: 550, Message: "Recipient belongs to a different user"}
		}
		if candidates[0].mailFail != nil {
			if response := candidates[0].mailFail.MatchRcpt(session.MailFrom, recipient); response != nil {
				return response
			}
		}
		session.OwnerUserID = candidates[0].userID
		return nil
	default:
		return &ResponseError{Code: 550, Message: "Recipient is ambiguous for configured users"}
	}
}

func (p *DomainPolicy) OnData(session *SessionMetadata) *ResponseError {
	if session == nil {
		return nil
	}
	if session.OwnerUserID == 0 {
		state := p.snapshot()
		if state.mailFail == nil {
			return nil
		}
		return state.mailFail.MatchData(session.MailFrom, session.RcptTo)
	}

	users, err := p.policyUsers()
	if err != nil {
		return &ResponseError{Code: 451, Message: "Temporary policy lookup failure"}
	}
	for _, user := range users {
		if user.userID != session.OwnerUserID {
			continue
		}
		if user.mailFail == nil {
			return nil
		}
		return user.mailFail.MatchData(session.MailFrom, session.RcptTo)
	}
	return nil
}

func (p *DomainPolicy) snapshot() domainPolicyState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

func (p *DomainPolicy) policyUsers() ([]domainPolicyState, error) {
	if p.store == nil {
		return nil, nil
	}
	ctx := context.Background()
	users, err := p.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	policies := make([]domainPolicyState, 0, len(users)+1)
	if adminSettings, ok, err := p.store.LoadAdminMailboxSettings(ctx); err != nil {
		return nil, err
	} else if ok && adminMailboxEnabled(adminSettings) {
		state, err := BuildUserPolicyState(0, adminSettings, p.store)
		if err != nil {
			return nil, err
		}
		policies = append(policies, state)
	}
	for _, user := range users {
		state, err := p.userState(user)
		if err != nil {
			return nil, err
		}
		policies = append(policies, state)
	}
	return policies, nil
}

func adminMailboxEnabled(settings models.AppSettings) bool {
	return mailboxPolicyEnabled(settings)
}

func mailboxPolicyEnabled(settings models.AppSettings) bool {
	return (settings.MailFailEnabled && len(settings.MailFailRules) > 0) ||
		strings.TrimSpace(settings.AllowedRemoteIPs) != "" ||
		strings.TrimSpace(settings.AcceptedRcptDomains) != "" ||
		strings.TrimSpace(settings.AcceptedFromDomains) != "" ||
		settings.AutoDeleteAfterDays > 0
}

func (p *DomainPolicy) userState(user models.User) (domainPolicyState, error) {
	config := DomainPolicyConfigFromSettings(user.Settings)
	state, err := buildDomainPolicyState(user.ID, config, p.store)
	if err != nil {
		return domainPolicyState{}, err
	}
	return state, nil
}

func connectAllowed(state domainPolicyState, session SessionMetadata) *ResponseError {
	if len(state.allowedRemoteNets) == 0 {
		return nil
	}

	ip := net.ParseIP(strings.TrimSpace(session.RemoteIP))
	if ip == nil {
		return &ResponseError{Code: 554, Message: "Connection not allowed"}
	}
	for _, network := range state.allowedRemoteNets {
		if network.Contains(ip) {
			return nil
		}
	}
	return &ResponseError{Code: 554, Message: "Connection not allowed"}
}

func matchesUserPolicy(state domainPolicyState, session SessionMetadata, recipient string) bool {
	if connectAllowed(state, session) != nil {
		return false
	}
	if !state.acceptedFromDomains.Empty() && !state.acceptedFromDomains.Match(session.MailFrom) {
		return false
	}
	if !state.acceptedRcptDomains.Empty() && !state.acceptedRcptDomains.Match(recipient) {
		return false
	}
	return true
}

func defaultRcptResponse(state domainPolicyState, session SessionMetadata, recipient string) *ResponseError {
	if response := connectAllowed(state, session); response != nil {
		return response
	}
	if state.mailFail != nil {
		if response := state.mailFail.MatchRcpt(session.MailFrom, recipient); response != nil {
			return response
		}
	}
	if !state.acceptedFromDomains.Empty() && !state.acceptedFromDomains.Match(session.MailFrom) {
		return &ResponseError{Code: 550, Message: "Sender domain not allowed"}
	}
	if !state.acceptedRcptDomains.Empty() && !state.acceptedRcptDomains.Match(recipient) {
		return &ResponseError{Code: 550, Message: "Recipient domain not allowed"}
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

func hasRecipientDomainRestrictions(states []domainPolicyState) bool {
	for _, state := range states {
		if !state.acceptedRcptDomains.Empty() {
			return true
		}
	}
	return false
}

func recipientOwnershipOverlaps(left, right domainPolicyState) bool {
	if left.acceptedRcptDomains.Empty() || right.acceptedRcptDomains.Empty() {
		return false
	}

	for domain := range left.acceptedRcptDomains.exactDomains {
		if right.acceptedRcptDomains.Match(domain) || right.acceptedRcptDomains.Match("probe@"+domain) {
			return true
		}
	}
	for domain := range right.acceptedRcptDomains.exactDomains {
		if left.acceptedRcptDomains.Match(domain) || left.acceptedRcptDomains.Match("probe@"+domain) {
			return true
		}
	}

	for _, pattern := range left.acceptedRcptDomains.patterns {
		for _, other := range right.acceptedRcptDomains.patterns {
			if pattern.String() == other.String() {
				return true
			}
		}
	}

	return false
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
