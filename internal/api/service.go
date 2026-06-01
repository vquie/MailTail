package api

import (
	"context"

	"github.com/vitaliquiering/mailtail/internal/models"
	"github.com/vitaliquiering/mailtail/internal/parser"
	"github.com/vitaliquiering/mailtail/internal/storage"
)

type Service struct {
	store  storage.Store
	parser *parser.Service
}

func NewService(store storage.Store, parser *parser.Service) *Service {
	return &Service{store: store, parser: parser}
}

func (s *Service) ListMessages(ctx context.Context, query string) ([]models.Message, error) {
	return s.store.ListMessages(ctx, models.MessageFilter{
		Query: query,
		Limit: 500,
	})
}

func (s *Service) GetMessage(ctx context.Context, id int64) (models.Message, error) {
	return s.store.GetMessage(ctx, id)
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
