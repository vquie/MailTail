package api

import (
	"context"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/parser"
	"github.com/vquie/MailTail/internal/storage"
)

type Service struct {
	store  storage.Store
	parser *parser.Service
}

func NewService(store storage.Store, parser *parser.Service) *Service {
	return &Service{store: store, parser: parser}
}

func (s *Service) ListMessages(ctx context.Context, query string) ([]models.Message, error) {
	messages, err := s.store.ListMessages(ctx, models.MessageFilter{
		Query: query,
		Limit: 500,
	})
	if err != nil {
		return nil, err
	}
	for i := range messages {
		s.parser.NormalizeMessage(&messages[i])
	}
	return messages, nil
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
