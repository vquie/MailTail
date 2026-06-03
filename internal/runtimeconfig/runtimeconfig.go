package runtimeconfig

import (
	"strings"
	"sync"

	"github.com/vquie/MailTail/internal/models"
)

type Manager struct {
	mu             sync.RWMutex
	settings       models.AppSettings
	allowedOrigins map[string]struct{}
}

func New(initial models.AppSettings) *Manager {
	manager := &Manager{}
	manager.Apply(initial)
	return manager
}

func (m *Manager) Apply(settings models.AppSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.settings = normalizeSettings(settings)
	m.allowedOrigins = csvToSet(m.settings.AllowedOrigins)
}

func (m *Manager) Settings() models.AppSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

func (m *Manager) AllowedOrigins() map[string]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.allowedOrigins) == 0 {
		return nil
	}

	cloned := make(map[string]struct{}, len(m.allowedOrigins))
	for key := range m.allowedOrigins {
		cloned[key] = struct{}{}
	}
	return cloned
}

func (m *Manager) SMTPLogVerbose() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.SMTPLogVerbose
}

func CSVList(value string) []string {
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

func normalizeSettings(settings models.AppSettings) models.AppSettings {
	settings.AllowedOrigins = normalizeCSV(settings.AllowedOrigins)
	settings.MailFailRulesFile = strings.TrimSpace(settings.MailFailRulesFile)
	settings.AllowedRemoteIPs = normalizeCSV(settings.AllowedRemoteIPs)
	settings.AcceptedRcptDomains = normalizeCSV(settings.AcceptedRcptDomains)
	settings.AcceptedFromDomains = normalizeCSV(settings.AcceptedFromDomains)
	return settings
}

func normalizeCSV(value string) string {
	return strings.Join(CSVList(value), ",")
}

func csvToSet(value string) map[string]struct{} {
	values := CSVList(value)
	if len(values) == 0 {
		return nil
	}

	set := make(map[string]struct{}, len(values))
	for _, entry := range values {
		set[entry] = struct{}{}
	}
	return set
}
