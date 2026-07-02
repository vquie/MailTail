package api

import (
	"context"
	"errors"
	"strings"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/parser"
	"github.com/vquie/MailTail/internal/runtimeconfig"
	"github.com/vquie/MailTail/internal/smtpserver"
	"github.com/vquie/MailTail/internal/storage"
)

var (
	ErrReservedUsername      = errors.New("username is reserved for the env admin")
	ErrUsernameRequired      = errors.New("username is required")
	ErrUsernameExists        = errors.New("username already exists")
	ErrPasswordRequired      = errors.New("password is required")
	ErrMailboxPolicyConflict = smtpserver.ErrRecipientOwnershipConflict
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

func (s *Service) ListMessages(ctx context.Context, principal models.SessionPrincipal, query, tag, cursor string, limit int) (models.MessagePage, error) {
	page, err := s.store.ListMessages(ctx, models.MessageFilter{
		Query:       query,
		Tag:         tag,
		Limit:       limit,
		Cursor:      cursor,
		OwnerUserID: principal.UserID,
		IncludeAll:  principal.IsAdmin,
	})
	if err != nil {
		return models.MessagePage{}, err
	}
	for i := range page.Messages {
		s.parser.NormalizeMessage(&page.Messages[i])
	}
	return page, nil
}

func (s *Service) GetMessage(ctx context.Context, principal models.SessionPrincipal, id int64) (models.Message, error) {
	message, err := s.store.GetMessage(ctx, id, principal)
	if err != nil {
		return models.Message{}, err
	}
	s.parser.NormalizeMessage(&message)
	return message, nil
}

func (s *Service) GetRawMessage(ctx context.Context, principal models.SessionPrincipal, id int64) (string, error) {
	return s.store.GetRawMessage(ctx, id, principal)
}

func (s *Service) GetAttachment(ctx context.Context, principal models.SessionPrincipal, messageID, attachmentID int64) (models.Attachment, []byte, error) {
	return s.store.GetAttachment(ctx, messageID, attachmentID, principal)
}

func (s *Service) DeleteMessage(ctx context.Context, principal models.SessionPrincipal, id int64) error {
	return s.store.DeleteMessage(ctx, id, principal)
}

func (s *Service) DeleteAllMessages(ctx context.Context, principal models.SessionPrincipal, query, tag string) error {
	return s.store.DeleteAllMessages(ctx, principal, models.MessageFilter{
		Query:       query,
		Tag:         tag,
		OwnerUserID: principal.UserID,
		IncludeAll:  principal.IsAdmin,
	})
}

func (s *Service) Stats(ctx context.Context, principal models.SessionPrincipal) (models.Stats, error) {
	return s.store.Stats(ctx, principal)
}

func (s *Service) AppInfo() models.AppInfo {
	return models.AppInfo{Version: s.version}
}

func (s *Service) Session(principal models.SessionPrincipal) models.SessionPrincipal {
	return principal
}

func (s *Service) Settings(ctx context.Context, principal models.SessionPrincipal) (models.AppSettings, error) {
	if principal.IsAdmin {
		if s.runtime == nil {
			return models.AppSettings{}, nil
		}
		return s.runtime.Settings(), nil
	}

	settings, ok, err := s.store.LoadUserSettings(ctx, principal.UserID)
	if err != nil {
		return models.AppSettings{}, err
	}
	if !ok {
		return models.AppSettings{}, nil
	}
	return settings, nil
}

func (s *Service) UpdateSettings(ctx context.Context, principal models.SessionPrincipal, settings models.AppSettings) (models.AppSettings, error) {
	if principal.IsAdmin {
		global := models.AppSettings{
			AllowedOrigins: strings.TrimSpace(settings.AllowedOrigins),
			SMTPLogVerbose: settings.SMTPLogVerbose,
		}
		if err := s.store.SaveAppSettings(ctx, global); err != nil {
			return models.AppSettings{}, err
		}
		if s.runtime != nil {
			s.runtime.Apply(global)
			return s.runtime.Settings(), nil
		}
		return global, nil
	}
	if settings.AutoDeleteAfterDays < 0 {
		settings.AutoDeleteAfterDays = 0
	}
	settings = runtimeconfig.NormalizeUserSettings(settings)
	if err := smtpserver.ValidateRecipientOwnership(ctx, principal.UserID, settings, s.store); err != nil {
		return models.AppSettings{}, err
	}
	if _, err := smtpserver.BuildUserPolicyState(principal.UserID, settings, s.store); err != nil {
		return models.AppSettings{}, err
	}

	if err := s.store.SaveUserSettings(ctx, principal.UserID, settings); err != nil {
		return models.AppSettings{}, err
	}
	if err := s.store.RecalculateMessageExpirations(ctx, principal.UserID, settings); err != nil {
		return models.AppSettings{}, err
	}
	return settings, nil
}

func (s *Service) AdminMailboxSettings(ctx context.Context) (models.AppSettings, error) {
	settings, ok, err := s.store.LoadAdminMailboxSettings(ctx)
	if err != nil {
		return models.AppSettings{}, err
	}
	if !ok {
		return models.AppSettings{}, nil
	}
	return settings, nil
}

func (s *Service) UpdateAdminMailboxSettings(ctx context.Context, settings models.AppSettings) (models.AppSettings, error) {
	if settings.AutoDeleteAfterDays < 0 {
		settings.AutoDeleteAfterDays = 0
	}
	settings = runtimeconfig.NormalizeUserSettings(settings)
	if err := smtpserver.ValidateRecipientOwnership(ctx, 0, settings, s.store); err != nil {
		return models.AppSettings{}, err
	}
	if _, err := smtpserver.BuildUserPolicyState(0, settings, s.store); err != nil {
		return models.AppSettings{}, err
	}
	if err := s.store.SaveAdminMailboxSettings(ctx, settings); err != nil {
		return models.AppSettings{}, err
	}
	if err := s.store.RecalculateMessageExpirations(ctx, 0, settings); err != nil {
		return models.AppSettings{}, err
	}
	return settings, nil
}

func (s *Service) ListUsers(ctx context.Context) ([]models.User, error) {
	return s.store.ListUsers(ctx)
}

func (s *Service) CreateUser(ctx context.Context, username, password string, settings models.AppSettings, reservedUsername string) (models.User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return models.User{}, ErrUsernameRequired
	}
	if password == "" {
		return models.User{}, ErrPasswordRequired
	}
	settings = runtimeconfig.NormalizeUserSettings(settings)
	if err := smtpserver.ValidateRecipientOwnership(ctx, -1, settings, s.store); err != nil {
		return models.User{}, err
	}
	if _, err := smtpserver.BuildUserPolicyState(0, settings, s.store); err != nil {
		return models.User{}, err
	}
	if strings.EqualFold(username, strings.TrimSpace(reservedUsername)) {
		return models.User{}, ErrReservedUsername
	}
	if _, ok, err := s.store.GetUserByUsername(ctx, username); err != nil {
		return models.User{}, err
	} else if ok {
		return models.User{}, ErrUsernameExists
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return models.User{}, err
	}
	return s.store.CreateUser(ctx, username, passwordHash, settings)
}

func (s *Service) UpdateUser(ctx context.Context, userID int64, username, password string, settings models.AppSettings, reservedUsername string) (models.User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return models.User{}, ErrUsernameRequired
	}
	settings = runtimeconfig.NormalizeUserSettings(settings)
	if err := smtpserver.ValidateRecipientOwnership(ctx, userID, settings, s.store); err != nil {
		return models.User{}, err
	}
	if _, err := smtpserver.BuildUserPolicyState(userID, settings, s.store); err != nil {
		return models.User{}, err
	}
	if strings.EqualFold(username, strings.TrimSpace(reservedUsername)) {
		return models.User{}, ErrReservedUsername
	}
	if existing, ok, err := s.store.GetUserByUsername(ctx, username); err != nil {
		return models.User{}, err
	} else if ok && existing.User.ID != userID {
		return models.User{}, ErrUsernameExists
	}
	user, err := s.store.UpdateUser(ctx, userID, username, settings)
	if err != nil {
		return models.User{}, err
	}
	if strings.TrimSpace(password) != "" {
		passwordHash, err := hashPassword(password)
		if err != nil {
			return models.User{}, err
		}
		if err := s.store.UpdateUserPassword(ctx, userID, passwordHash); err != nil {
			return models.User{}, err
		}
		user, _, err = s.store.GetUser(ctx, userID)
		if err != nil {
			return models.User{}, err
		}
	}
	if err := s.store.RecalculateMessageExpirations(ctx, userID, settings); err != nil {
		return models.User{}, err
	}
	return user, nil
}

func (s *Service) DeleteUser(ctx context.Context, userID int64) error {
	return s.store.DeleteUser(ctx, userID)
}
