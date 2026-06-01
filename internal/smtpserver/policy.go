package smtpserver

import (
	"net"
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
	acceptedRcptDomains map[string]struct{}
	acceptedFromDomains map[string]struct{}
	allowedRemoteNets   []*net.IPNet
}

func NewDomainPolicy(acceptedRcptDomains, acceptedFromDomains, allowedRemoteCIDRs []string) (*DomainPolicy, error) {
	allowedRemoteNets, err := parseAllowedRemoteCIDRs(allowedRemoteCIDRs)
	if err != nil {
		return nil, err
	}

	return &DomainPolicy{
		acceptedRcptDomains: normalizeDomainSet(acceptedRcptDomains),
		acceptedFromDomains: normalizeDomainSet(acceptedFromDomains),
		allowedRemoteNets:   allowedRemoteNets,
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

func (p *DomainPolicy) OnData(SessionMetadata) *ResponseError { return nil }

func (p *DomainPolicy) OnMailFrom(_ SessionMetadata, from string) *ResponseError {
	if len(p.acceptedFromDomains) == 0 {
		return nil
	}
	domain, ok := extractDomain(from)
	if !ok || !p.isAllowed(p.acceptedFromDomains, domain) {
		return &ResponseError{
			Code:    550,
			Message: "Sender domain not allowed",
		}
	}
	return nil
}

func (p *DomainPolicy) OnRcptTo(_ SessionMetadata, recipient string) *ResponseError {
	if len(p.acceptedRcptDomains) == 0 {
		return nil
	}
	domain, ok := extractDomain(recipient)
	if !ok || !p.isAllowed(p.acceptedRcptDomains, domain) {
		return &ResponseError{
			Code:    550,
			Message: "Recipient domain not allowed",
		}
	}
	return nil
}

func (p *DomainPolicy) isAllowed(allowed map[string]struct{}, domain string) bool {
	_, ok := allowed[strings.ToLower(strings.TrimSpace(domain))]
	return ok
}

func normalizeDomainSet(domains []string) map[string]struct{} {
	set := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		set[domain] = struct{}{}
	}
	return set
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
