package runtimeconfig

import (
	"strings"
	"sync"

	"github.com/vquie/MailTail/internal/models"
)

const DefaultAutoDeleteDays = 30

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

func (m *Manager) AutoDeleteAfterDays() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.AutoDeleteAfterDays
}

func CSVList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
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

func NormalizeUserSettings(settings models.AppSettings) models.AppSettings {
	settings.AllowedOrigins = ""
	settings.SMTPLogVerbose = false
	return normalizeSettings(settings)
}

func normalizeSettings(settings models.AppSettings) models.AppSettings {
	settings.AllowedOrigins = normalizeCSV(settings.AllowedOrigins)
	settings.MailFailRules = normalizeMailFailRules(settings.MailFailRules)
	settings.ReportFrom = strings.TrimSpace(settings.ReportFrom)
	settings.AllowedRemoteIPs = normalizeCSV(settings.AllowedRemoteIPs)
	settings.AcceptedRcptDomains = normalizeCSV(settings.AcceptedRcptDomains)
	settings.AcceptedFromDomains = normalizeCSV(settings.AcceptedFromDomains)
	if settings.AutoDeleteAfterDays < 0 {
		settings.AutoDeleteAfterDays = 0
	}
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

func normalizeMailFailRules(rules []models.MailFailRule) []models.MailFailRule {
	if len(rules) == 0 {
		return nil
	}

	normalized := make([]models.MailFailRule, 0, len(rules))
	for _, rule := range rules {
		action := strings.ToLower(strings.TrimSpace(rule.Action))
		stage := strings.TrimSpace(rule.Stage)
		message := strings.TrimSpace(rule.Message)
		feedbackType := strings.ToLower(strings.TrimSpace(rule.FeedbackType))
		reportRecipientLocalPart := strings.TrimSpace(rule.ReportRecipientLocalPart)
		if isReportAction(action) {
			stage = "data"
			if action != "async-bounce" {
				message = ""
			}
		}
		if action != "arf" {
			feedbackType = ""
		}
		if !supportsReportRecipientLocalPart(action) {
			reportRecipientLocalPart = ""
		}
		normalized = append(normalized, models.MailFailRule{
			Name:                     strings.TrimSpace(rule.Name),
			Trigger:                  strings.TrimSpace(rule.Trigger),
			Stage:                    stage,
			Action:                   action,
			AllowAfter:               rule.AllowAfter,
			MinRetryAfter:            strings.TrimSpace(rule.MinRetryAfter),
			ResetAfter:               strings.TrimSpace(rule.ResetAfter),
			Code:                     rule.Code,
			EnhancedCode:             strings.TrimSpace(rule.EnhancedCode),
			FeedbackType:             feedbackType,
			ReportRecipientLocalPart: reportRecipientLocalPart,
			Message:                  message,
		})
	}
	return normalized
}

func supportsReportRecipientLocalPart(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "arf", "xarf-v3", "xarf-v4":
		return true
	default:
		return false
	}
}

func isReportAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "arf", "xarf-v3", "xarf-v4", "original-report", "async-bounce":
		return true
	default:
		return false
	}
}
