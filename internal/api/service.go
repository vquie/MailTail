package api

import (
	"context"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/parser"
	"github.com/vquie/MailTail/internal/runtimeconfig"
	"github.com/vquie/MailTail/internal/smtpserver"
	"github.com/vquie/MailTail/internal/storage"
)

type Service struct {
	store   storage.Store
	parser  *parser.Service
	version string
	runtime *runtimeconfig.Manager
	policy  *smtpserver.DomainPolicy
}

func NewService(store storage.Store, parser *parser.Service, version string, runtime *runtimeconfig.Manager, policy *smtpserver.DomainPolicy) *Service {
	return &Service{store: store, parser: parser, version: version, runtime: runtime, policy: policy}
}

func (s *Service) ListMessages(ctx context.Context, query, cursor string, limit int) (models.MessagePage, error) {
	page, err := s.store.ListMessages(ctx, models.MessageFilter{
		Query:  query,
		Limit:  limit,
		Cursor: cursor,
	})
	if err != nil {
		return models.MessagePage{}, err
	}
	for i := range page.Messages {
		s.parser.NormalizeMessage(&page.Messages[i])
	}
	return page, nil
}

func (s *Service) GetMessage(ctx context.Context, id int64) (models.Message, error) {
	message, err := s.store.GetMessage(ctx, id)
	if err != nil {
		return models.Message{}, err
	}
	s.parser.NormalizeMessage(&message)
	return message, nil
}

func (s *Service) GetRawMessage(ctx context.Context, id int64) (string, error) {
	return s.store.GetRawMessage(ctx, id)
}

func (s *Service) GetAttachment(ctx context.Context, messageID, attachmentID int64) (models.Attachment, []byte, error) {
	return s.store.GetAttachment(ctx, messageID, attachmentID)
}

func (s *Service) DeleteMessage(ctx context.Context, id int64) error {
	return s.store.DeleteMessage(ctx, id)
}

func (s *Service) DeleteAllMessages(ctx context.Context) error {
	return s.store.DeleteAllMessages(ctx)
}

func (s *Service) Stats(ctx context.Context) (models.Stats, error) {
	return s.store.Stats(ctx)
}

func (s *Service) AppInfo() models.AppInfo {
	return models.AppInfo{Version: s.version}
}

func (s *Service) Settings() models.AppSettings {
	if s.runtime == nil {
		return models.AppSettings{}
	}
	return s.runtime.Settings()
}

func (s *Service) UpdateSettings(ctx context.Context, settings models.AppSettings) (models.AppSettings, error) {
	if settings.AutoDeleteAfterDays < 0 {
		settings.AutoDeleteAfterDays = 0
	}
	config := smtpserver.DomainPolicyConfigFromSettings(settings)
	if _, err := smtpserver.NewDomainPolicy(config, nil); err != nil {
		return models.AppSettings{}, err
	}

	if err := s.store.SaveAppSettings(ctx, settings); err != nil {
		return models.AppSettings{}, err
	}

	if s.policy != nil {
		if err := s.policy.ApplyConfig(config); err != nil {
			return models.AppSettings{}, err
		}
	}
	if s.runtime != nil {
		s.runtime.Apply(settings)
		return s.runtime.Settings(), nil
	}
	return settings, nil
}
