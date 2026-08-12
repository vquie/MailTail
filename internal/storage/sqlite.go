package storage

import (
	"context"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/vquie/MailTail/internal/models"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var ErrNotFound = errors.New("not found")
var ErrInvalidCursor = errors.New("invalid cursor")
var ftsTokenPattern = regexp.MustCompile(`[[:alnum:]_.@+-]+`)

type Store interface {
	CreateMessage(ctx context.Context, message models.StoredMessage) (int64, error)
	ListMessages(ctx context.Context, filter models.MessageFilter) (models.MessagePage, error)
	LoadAppSettings(ctx context.Context) (models.AppSettings, bool, error)
	SaveAppSettings(ctx context.Context, settings models.AppSettings) error
	LoadAdminMailboxSettings(ctx context.Context) (models.AppSettings, bool, error)
	SaveAdminMailboxSettings(ctx context.Context, settings models.AppSettings) error
	LoadUserSettings(ctx context.Context, userID int64) (models.AppSettings, bool, error)
	SaveUserSettings(ctx context.Context, userID int64, settings models.AppSettings) error
	CreateUser(ctx context.Context, username, passwordHash string, settings models.AppSettings) (models.User, error)
	UpdateUser(ctx context.Context, userID int64, username string, settings models.AppSettings) (models.User, error)
	UpdateUserPassword(ctx context.Context, userID int64, passwordHash string) error
	DeleteUser(ctx context.Context, userID int64) error
	ListUsers(ctx context.Context) ([]models.User, error)
	GetUser(ctx context.Context, userID int64) (models.User, bool, error)
	GetUserByUsername(ctx context.Context, username string) (models.UserCredentials, bool, error)
	DeleteExpiredMessages(ctx context.Context, before time.Time) (int64, error)
	RecalculateMessageExpirations(ctx context.Context, userID int64, settings models.AppSettings) error
	CreateAuthSession(ctx context.Context, session models.AuthSession) error
	GetAuthSession(ctx context.Context, sessionID string) (models.AuthSession, bool, error)
	DeleteAuthSession(ctx context.Context, sessionID string) error
	DeleteExpiredAuthSessions(ctx context.Context, before time.Time) error
	CountLoginAttemptsSince(ctx context.Context, clientKey string, since time.Time) (int, error)
	RecordLoginAttempt(ctx context.Context, clientKey string, at time.Time) error
	ClearLoginAttempts(ctx context.Context, clientKey string) error
	DeleteExpiredLoginAttempts(ctx context.Context, before time.Time) error
	GetGreylistState(ctx context.Context, key string) (models.GreylistState, bool, error)
	SaveGreylistState(ctx context.Context, state models.GreylistState) error
	DeleteGreylistState(ctx context.Context, key string) error
	DeleteExpiredGreylistStates(ctx context.Context, before time.Time) error
	EnqueueOutboundMessage(ctx context.Context, message models.OutboundMessage) error
	ListDueOutboundMessages(ctx context.Context, before time.Time, limit int) ([]models.OutboundMessage, error)
	DeleteOutboundMessage(ctx context.Context, id int64) error
	RescheduleOutboundMessage(ctx context.Context, id int64, attempts int, nextAttempt time.Time, lastError string) error
	DeferOutboundMessagesForDomain(ctx context.Context, domain string, exceptID int64, nextAttempt time.Time, lastError string) error
	GetMessage(ctx context.Context, id int64, principal models.SessionPrincipal) (models.Message, error)
	GetRawMessage(ctx context.Context, id int64, principal models.SessionPrincipal) (string, error)
	GetAttachment(ctx context.Context, messageID, attachmentID int64, principal models.SessionPrincipal) (models.Attachment, []byte, error)
	DeleteMessage(ctx context.Context, id int64, principal models.SessionPrincipal) error
	DeleteAllMessages(ctx context.Context, principal models.SessionPrincipal, filter models.MessageFilter) error
	Stats(ctx context.Context, principal models.SessionPrincipal) (models.Stats, error)
	Close() error
}

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA temp_store = MEMORY",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return nil, err
		}
	}

	if _, err := db.Exec("PRAGMA wal_autocheckpoint = 1000"); err != nil {
		return nil, err
	}

	schema, err := migrationsFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(string(schema)); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	store := &SQLiteStore{db: db}
	if err := store.applySchemaUpgrades(); err != nil {
		return nil, fmt.Errorf("apply schema upgrades: %w", err)
	}
	if err := store.backfillMessageTags(); err != nil {
		return nil, fmt.Errorf("backfill message tags: %w", err)
	}

	if _, err := db.Exec(`INSERT INTO messages_fts(messages_fts) VALUES ('rebuild')`); err != nil {
		return nil, fmt.Errorf("rebuild message search index: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) applySchemaUpgrades() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS user_settings (
			user_id INTEGER PRIMARY KEY,
			value_json TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}

	if err := s.ensureColumn("messages", "owner_user_id", `ALTER TABLE messages ADD COLUMN owner_user_id INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn("messages", "expires_at", `ALTER TABLE messages ADD COLUMN expires_at TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn("auth_sessions", "user_id", `ALTER TABLE auth_sessions ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn("auth_sessions", "is_admin", `ALTER TABLE auth_sessions ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 1`); err != nil {
		return err
	}

	indexStatements := []string{
		`CREATE INDEX IF NOT EXISTS idx_messages_owner_received_at ON messages(owner_user_id, received_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_expires_at ON messages(expires_at)`,
		`CREATE TABLE IF NOT EXISTS message_tags (
			message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
			tag TEXT NOT NULL,
			PRIMARY KEY (message_id, tag)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_message_tags_tag_message_id ON message_tags(tag, message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_message_tags_message_id ON message_tags(message_id)`,
		`CREATE TABLE IF NOT EXISTS outbound_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			envelope_from TEXT NOT NULL,
			recipient TEXT NOT NULL,
			raw TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			next_attempt TEXT NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_outbound_messages_next_attempt ON outbound_messages(next_attempt, id)`,
	}
	for _, statement := range indexStatements {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) EnqueueOutboundMessage(ctx context.Context, message models.OutboundMessage) error {
	nextAttempt := message.NextAttempt
	if nextAttempt.IsZero() {
		nextAttempt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO outbound_messages (envelope_from, recipient, raw, attempts, next_attempt, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, message.EnvelopeFrom, message.Recipient, message.Raw, message.Attempts, nextAttempt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) ListDueOutboundMessages(ctx context.Context, before time.Time, limit int) ([]models.OutboundMessage, error) {
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, envelope_from, recipient, raw, attempts, next_attempt
		FROM outbound_messages
		WHERE next_attempt <= ?
		ORDER BY next_attempt, id
		LIMIT ?
	`, before.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]models.OutboundMessage, 0)
	for rows.Next() {
		var message models.OutboundMessage
		var nextAttempt string
		if err := rows.Scan(&message.ID, &message.EnvelopeFrom, &message.Recipient, &message.Raw, &message.Attempts, &nextAttempt); err != nil {
			return nil, err
		}
		message.NextAttempt, err = time.Parse(time.RFC3339Nano, nextAttempt)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *SQLiteStore) DeleteOutboundMessage(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM outbound_messages WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) RescheduleOutboundMessage(ctx context.Context, id int64, attempts int, nextAttempt time.Time, lastError string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE outbound_messages
		SET attempts = ?, next_attempt = ?, last_error = ?
		WHERE id = ?
	`, attempts, nextAttempt.UTC().Format(time.RFC3339Nano), lastError, id)
	return err
}

func (s *SQLiteStore) DeferOutboundMessagesForDomain(ctx context.Context, domain string, exceptID int64, nextAttempt time.Time, lastError string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return fmt.Errorf("outbound recipient domain is required")
	}
	retryAt := nextAttempt.UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		UPDATE outbound_messages
		SET next_attempt = CASE WHEN julianday(next_attempt) < julianday(?) THEN ? ELSE next_attempt END,
			last_error = ?
		WHERE id != ?
			AND instr(recipient, '@') > 1
			AND lower(substr(recipient, instr(recipient, '@') + 1)) = ?
	`, retryAt, retryAt, lastError, exceptID, domain)
	return err
}

func (s *SQLiteStore) ensureColumn(table, column, alter string) error {
	columns, err := s.tableColumns(table)
	if err != nil {
		return err
	}
	if _, exists := columns[column]; exists {
		return nil
	}
	_, err = s.db.Exec(alter)
	return err
}

func (s *SQLiteStore) tableColumns(table string) (map[string]struct{}, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	return columns, rows.Err()
}

func (s *SQLiteStore) CreateMessage(ctx context.Context, message models.StoredMessage) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rcptJSON, err := json.Marshal(message.RcptTo)
	if err != nil {
		return 0, err
	}
	headersJSON, err := json.Marshal(message.Headers)
	if err != nil {
		return 0, err
	}
	message.Tags = models.NormalizeTags(message.Tags)
	if len(message.Tags) == 0 {
		message.Tags = models.ExtractTagsFromRecipients(message.RcptTo)
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO messages (
			owner_user_id, expires_at, received_at, mail_from, rcpt_to_json, header_from, header_to,
			subject, message_id, helo, remote_ip, size, raw, text_body, html_body, headers_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		message.OwnerUserID,
		formatOptionalTime(message.ExpiresAt),
		message.ReceivedAt.UTC().Format(time.RFC3339Nano),
		message.MailFrom,
		string(rcptJSON),
		message.HeaderFrom,
		message.HeaderTo,
		message.Subject,
		message.MessageID,
		message.Helo,
		message.RemoteIP,
		message.Size,
		message.Raw,
		message.TextBody,
		message.HTMLBody,
		string(headersJSON),
	)
	if err != nil {
		return 0, err
	}

	messageID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	for _, attachment := range message.Attachments {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO attachments (message_id, file_name, content_type, content_id, size, inline, content)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`,
			messageID,
			attachment.FileName,
			attachment.ContentType,
			attachment.ContentID,
			attachment.Size,
			boolToInt(attachment.Inline),
			attachment.Content,
		); err != nil {
			return 0, err
		}
	}

	for _, tag := range message.Tags {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_tags (message_id, tag)
			VALUES (?, ?)
		`, messageID, tag); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return messageID, nil
}

func (s *SQLiteStore) LoadAppSettings(ctx context.Context) (models.AppSettings, bool, error) {
	return s.loadNamedSettings(ctx, "runtime")
}

func (s *SQLiteStore) SaveAppSettings(ctx context.Context, settings models.AppSettings) error {
	return s.saveNamedSettings(ctx, "runtime", settings)
}

func (s *SQLiteStore) LoadAdminMailboxSettings(ctx context.Context) (models.AppSettings, bool, error) {
	return s.loadNamedSettings(ctx, "admin_mailbox")
}

func (s *SQLiteStore) SaveAdminMailboxSettings(ctx context.Context, settings models.AppSettings) error {
	return s.saveNamedSettings(ctx, "admin_mailbox", settings)
}

func (s *SQLiteStore) loadNamedSettings(ctx context.Context, key string) (models.AppSettings, bool, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key = ?`, key).Scan(&payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.AppSettings{}, false, nil
		}
		return models.AppSettings{}, false, err
	}

	var settings models.AppSettings
	if err := json.Unmarshal([]byte(payload), &settings); err != nil {
		return models.AppSettings{}, false, err
	}
	return settings, true, nil
}

func (s *SQLiteStore) saveNamedSettings(ctx context.Context, key string, settings models.AppSettings) error {
	payload, err := json.Marshal(settings)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO app_settings (key, value_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value_json = excluded.value_json,
			updated_at = excluded.updated_at
	`, key, string(payload), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) LoadUserSettings(ctx context.Context, userID int64) (models.AppSettings, bool, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM user_settings WHERE user_id = ?`, userID).Scan(&payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.AppSettings{}, false, nil
		}
		return models.AppSettings{}, false, err
	}

	var settings models.AppSettings
	if err := json.Unmarshal([]byte(payload), &settings); err != nil {
		return models.AppSettings{}, false, err
	}
	return settings, true, nil
}

func (s *SQLiteStore) SaveUserSettings(ctx context.Context, userID int64, settings models.AppSettings) error {
	payload, err := json.Marshal(settings)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO user_settings (user_id, value_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			value_json = excluded.value_json,
			updated_at = excluded.updated_at
	`, userID, string(payload), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) CreateUser(ctx context.Context, username, passwordHash string, settings models.AppSettings) (models.User, error) {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.User{}, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, username, passwordHash, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return models.User{}, err
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return models.User{}, err
	}

	payload, err := json.Marshal(settings)
	if err != nil {
		return models.User{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_settings (user_id, value_json, updated_at)
		VALUES (?, ?, ?)
	`, userID, string(payload), now.Format(time.RFC3339Nano)); err != nil {
		return models.User{}, err
	}

	if err := tx.Commit(); err != nil {
		return models.User{}, err
	}
	return models.User{ID: userID, Username: username, Settings: settings, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *SQLiteStore) UpdateUser(ctx context.Context, userID int64, username string, settings models.AppSettings) (models.User, error) {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.User{}, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `UPDATE users SET username = ?, updated_at = ? WHERE id = ?`, username, now.Format(time.RFC3339Nano), userID)
	if err != nil {
		return models.User{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return models.User{}, err
	}
	if rows == 0 {
		return models.User{}, ErrNotFound
	}

	payload, err := json.Marshal(settings)
	if err != nil {
		return models.User{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_settings (user_id, value_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			value_json = excluded.value_json,
			updated_at = excluded.updated_at
	`, userID, string(payload), now.Format(time.RFC3339Nano)); err != nil {
		return models.User{}, err
	}

	if err := tx.Commit(); err != nil {
		return models.User{}, err
	}
	return s.getUserByID(ctx, userID)
}

func (s *SQLiteStore) UpdateUserPassword(ctx context.Context, userID int64, passwordHash string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, time.Now().UTC().Format(time.RFC3339Nano), userID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) DeleteUser(ctx context.Context, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_settings WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE owner_user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) ListUsers(ctx context.Context) ([]models.User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT users.id, users.username, users.created_at, users.updated_at, COALESCE(user_settings.value_json, '{}')
		FROM users
		LEFT JOIN user_settings ON user_settings.user_id = users.id
		ORDER BY users.username ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]models.User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) GetUser(ctx context.Context, userID int64) (models.User, bool, error) {
	user, err := s.getUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return models.User{}, false, nil
		}
		return models.User{}, false, err
	}
	return user, true, nil
}

func (s *SQLiteStore) getUserByID(ctx context.Context, userID int64) (models.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT users.id, users.username, users.created_at, users.updated_at, COALESCE(user_settings.value_json, '{}')
		FROM users
		LEFT JOIN user_settings ON user_settings.user_id = users.id
		WHERE users.id = ?
	`, userID)
	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.User{}, ErrNotFound
		}
		return models.User{}, err
	}
	return user, nil
}

func (s *SQLiteStore) GetUserByUsername(ctx context.Context, username string) (models.UserCredentials, bool, error) {
	var (
		user         models.User
		passwordHash string
		createdAt    string
		updatedAt    string
		settingsJSON string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT users.id, users.username, users.password_hash, users.created_at, users.updated_at, COALESCE(user_settings.value_json, '{}')
		FROM users
		LEFT JOIN user_settings ON user_settings.user_id = users.id
		WHERE LOWER(users.username) = LOWER(?)
	`, username).Scan(&user.ID, &user.Username, &passwordHash, &createdAt, &updatedAt, &settingsJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.UserCredentials{}, false, nil
		}
		return models.UserCredentials{}, false, err
	}

	if err := hydrateUser(&user, createdAt, updatedAt, settingsJSON); err != nil {
		return models.UserCredentials{}, false, err
	}
	return models.UserCredentials{User: user, PasswordHash: passwordHash}, true, nil
}

func (s *SQLiteStore) DeleteExpiredMessages(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE expires_at != '' AND expires_at <= ?`, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return rows, nil
}

func (s *SQLiteStore) RecalculateMessageExpirations(ctx context.Context, userID int64, settings models.AppSettings) error {
	expiresExpr := ""
	if settings.AutoDeleteAfterDays > 0 {
		interval := time.Duration(settings.AutoDeleteAfterDays) * 24 * time.Hour
		rows, err := s.db.QueryContext(ctx, `SELECT id, received_at FROM messages WHERE owner_user_id = ?`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()

		type updateRow struct {
			id        int64
			expiresAt string
		}
		updates := make([]updateRow, 0)
		for rows.Next() {
			var (
				id         int64
				receivedAt string
			)
			if err := rows.Scan(&id, &receivedAt); err != nil {
				return err
			}
			parsed, err := time.Parse(time.RFC3339Nano, receivedAt)
			if err != nil {
				return err
			}
			updates = append(updates, updateRow{
				id:        id,
				expiresAt: parsed.Add(interval).UTC().Format(time.RFC3339Nano),
			})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, update := range updates {
			if _, err := tx.ExecContext(ctx, `UPDATE messages SET expires_at = ? WHERE id = ?`, update.expiresAt, update.id); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET expires_at = ? WHERE owner_user_id = ?`, expiresExpr, userID)
	return err
}

func (s *SQLiteStore) CreateAuthSession(ctx context.Context, session models.AuthSession) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_sessions (session_id, username, user_id, is_admin, csrf_token, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, session.SessionID, session.Username, session.UserID, boolToInt(session.IsAdmin), session.CSRFToken, session.ExpiresAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) GetAuthSession(ctx context.Context, sessionID string) (models.AuthSession, bool, error) {
	var session models.AuthSession
	var expiresAt string
	var isAdminInt int
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, username, user_id, is_admin, csrf_token, expires_at
		FROM auth_sessions
		WHERE session_id = ?
	`, sessionID).Scan(&session.SessionID, &session.Username, &session.UserID, &isAdminInt, &session.CSRFToken, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.AuthSession{}, false, nil
		}
		return models.AuthSession{}, false, err
	}

	parsed, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return models.AuthSession{}, false, err
	}
	session.ExpiresAt = parsed
	session.IsAdmin = isAdminInt == 1
	return session, true, nil
}

func (s *SQLiteStore) DeleteAuthSession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE session_id = ?`, sessionID)
	return err
}

func (s *SQLiteStore) DeleteExpiredAuthSessions(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expires_at <= ?`, before.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) CountLoginAttemptsSince(ctx context.Context, clientKey string, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM auth_login_attempts
		WHERE client_key = ? AND attempted_at > ?
	`, clientKey, since.UTC().Format(time.RFC3339Nano)).Scan(&count)
	return count, err
}

func (s *SQLiteStore) RecordLoginAttempt(ctx context.Context, clientKey string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_login_attempts (client_key, attempted_at)
		VALUES (?, ?)
	`, clientKey, at.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) ClearLoginAttempts(ctx context.Context, clientKey string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_login_attempts WHERE client_key = ?`, clientKey)
	return err
}

func (s *SQLiteStore) DeleteExpiredLoginAttempts(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_login_attempts WHERE attempted_at <= ?`, before.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) GetGreylistState(ctx context.Context, key string) (models.GreylistState, bool, error) {
	var (
		state     models.GreylistState
		firstSeen string
		lastSeen  string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT state_key, first_seen, last_seen, attempts
		FROM greylist_states
		WHERE state_key = ?
	`, key).Scan(&state.Key, &firstSeen, &lastSeen, &state.Attempts)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.GreylistState{}, false, nil
		}
		return models.GreylistState{}, false, err
	}

	var parseErr error
	state.FirstSeen, parseErr = time.Parse(time.RFC3339Nano, firstSeen)
	if parseErr != nil {
		return models.GreylistState{}, false, parseErr
	}
	state.LastSeen, parseErr = time.Parse(time.RFC3339Nano, lastSeen)
	if parseErr != nil {
		return models.GreylistState{}, false, parseErr
	}
	return state, true, nil
}

func (s *SQLiteStore) SaveGreylistState(ctx context.Context, state models.GreylistState) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO greylist_states (state_key, first_seen, last_seen, attempts)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(state_key) DO UPDATE SET
			first_seen = excluded.first_seen,
			last_seen = excluded.last_seen,
			attempts = excluded.attempts
	`, state.Key, state.FirstSeen.UTC().Format(time.RFC3339Nano), state.LastSeen.UTC().Format(time.RFC3339Nano), state.Attempts)
	return err
}

func (s *SQLiteStore) DeleteGreylistState(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM greylist_states WHERE state_key = ?`, key)
	return err
}

func (s *SQLiteStore) DeleteExpiredGreylistStates(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM greylist_states WHERE last_seen <= ?`, before.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) ListMessages(ctx context.Context, filter models.MessageFilter) (models.MessagePage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 25
	}

	query := `
		SELECT messages.id, messages.owner_user_id, messages.received_at, messages.mail_from, messages.rcpt_to_json,
		       messages.header_from, messages.header_to, messages.subject,
		       messages.message_id, messages.helo, messages.remote_ip, messages.size
		FROM messages
	`
	args := make([]any, 0, 6)
	clauses := make([]string, 0, 2)
	if !filter.IncludeAll {
		clauses = append(clauses, `messages.owner_user_id = ?`)
		args = append(args, filter.OwnerUserID)
	}
	if strings.TrimSpace(filter.Query) != "" {
		ftsQuery := buildFTSQuery(filter.Query)
		if ftsQuery != "" {
			query += `
				JOIN messages_fts ON messages_fts.rowid = messages.id
			`
			clauses = append(clauses, `messages_fts MATCH ?`)
			args = append(args, ftsQuery)
		} else {
			clauses = append(clauses, `(LOWER(messages.subject) LIKE ? OR LOWER(messages.header_from) LIKE ? OR LOWER(messages.header_to) LIKE ?)`)
			term := "%" + strings.ToLower(strings.TrimSpace(filter.Query)) + "%"
			args = append(args, term, term, term)
		}
	}
	if tag := strings.ToLower(strings.TrimSpace(filter.Tag)); tag != "" {
		clauses = append(clauses, `EXISTS (SELECT 1 FROM message_tags WHERE message_tags.message_id = messages.id AND message_tags.tag = ?)`)
		args = append(args, tag)
	}

	if filter.Cursor != "" {
		cursorTime, cursorID, err := decodeMessageCursor(filter.Cursor)
		if err != nil {
			return models.MessagePage{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		clauses = append(clauses, `((messages.received_at < ?) OR (messages.received_at = ? AND messages.id < ?))`)
		args = append(args, cursorTime, cursorTime, cursorID)
	}

	if len(clauses) > 0 {
		query += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	query += ` ORDER BY messages.received_at DESC, messages.id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return models.MessagePage{}, err
	}
	defer rows.Close()

	messages := make([]models.Message, 0, limit)
	for rows.Next() {
		msg, err := scanMessageSummary(rows)
		if err != nil {
			return models.MessagePage{}, err
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return models.MessagePage{}, err
	}

	page := models.MessagePage{Messages: messages}
	if err := s.hydrateTagsForMessages(ctx, page.Messages); err != nil {
		return models.MessagePage{}, err
	}
	availableTags, err := s.listAvailableTags(ctx, filter)
	if err != nil {
		return models.MessagePage{}, err
	}
	page.AvailableTags = availableTags
	if len(messages) > limit {
		page.HasMore = true
		page.Messages = messages[:limit]
		last := page.Messages[len(page.Messages)-1]
		page.NextCursor = encodeMessageCursor(last.ReceivedAt.UTC().Format(time.RFC3339Nano), last.ID)
	}
	if len(page.Messages) == 0 {
		page.Messages = []models.Message{}
	}

	return page, nil
}

func (s *SQLiteStore) GetMessage(ctx context.Context, id int64, principal models.SessionPrincipal) (models.Message, error) {
	query := `
		SELECT id, owner_user_id, received_at, mail_from, rcpt_to_json, header_from, header_to, subject,
		       message_id, helo, remote_ip, size, raw, text_body, html_body, headers_json
		FROM messages WHERE id = ?
	`
	args := []any{id}
	if !principal.IsAdmin {
		query += ` AND owner_user_id = ?`
		args = append(args, principal.UserID)
	}
	row := s.db.QueryRowContext(ctx, query, args...)

	var (
		msg         models.Message
		receivedAt  string
		rcptJSON    string
		headersJSON string
	)
	if err := row.Scan(
		&msg.ID,
		&msg.OwnerUserID,
		&receivedAt,
		&msg.MailFrom,
		&rcptJSON,
		&msg.HeaderFrom,
		&msg.HeaderTo,
		&msg.Subject,
		&msg.MessageID,
		&msg.Helo,
		&msg.RemoteIP,
		&msg.Size,
		&msg.Raw,
		&msg.TextBody,
		&msg.HTMLBody,
		&headersJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Message{}, ErrNotFound
		}
		return models.Message{}, err
	}

	if err := hydrateMessage(&msg, receivedAt, rcptJSON, headersJSON); err != nil {
		return models.Message{}, err
	}
	messages := []models.Message{msg}
	if err := s.hydrateTagsForMessages(ctx, messages); err != nil {
		return models.Message{}, err
	}
	msg = messages[0]

	attachments, err := s.listAttachments(ctx, msg.ID)
	if err != nil {
		return models.Message{}, err
	}
	msg.Attachments = attachments
	return msg, nil
}

func (s *SQLiteStore) GetRawMessage(ctx context.Context, id int64, principal models.SessionPrincipal) (string, error) {
	var raw string
	query := `SELECT raw FROM messages WHERE id = ?`
	args := []any{id}
	if !principal.IsAdmin {
		query += ` AND owner_user_id = ?`
		args = append(args, principal.UserID)
	}
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return raw, nil
}

func (s *SQLiteStore) GetAttachment(ctx context.Context, messageID, attachmentID int64, principal models.SessionPrincipal) (models.Attachment, []byte, error) {
	query := `
		SELECT id, message_id, file_name, content_type, content_id, size, inline, content
		FROM attachments
		WHERE message_id = ? AND id = ?
	`
	args := []any{messageID, attachmentID}
	if !principal.IsAdmin {
		query = `
			SELECT attachments.id, attachments.message_id, attachments.file_name, attachments.content_type, attachments.content_id, attachments.size, attachments.inline, attachments.content
			FROM attachments
			JOIN messages ON messages.id = attachments.message_id
			WHERE attachments.message_id = ? AND attachments.id = ? AND messages.owner_user_id = ?
		`
		args = append(args, principal.UserID)
	}
	row := s.db.QueryRowContext(ctx, query, args...)

	var (
		attachment models.Attachment
		inlineInt  int
		content    []byte
	)
	if err := row.Scan(
		&attachment.ID,
		&attachment.MessageID,
		&attachment.FileName,
		&attachment.ContentType,
		&attachment.ContentID,
		&attachment.Size,
		&inlineInt,
		&content,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Attachment{}, nil, ErrNotFound
		}
		return models.Attachment{}, nil, err
	}
	attachment.Inline = inlineInt == 1
	return attachment, content, nil
}

func (s *SQLiteStore) DeleteMessage(ctx context.Context, id int64, principal models.SessionPrincipal) error {
	query := `DELETE FROM messages WHERE id = ?`
	args := []any{id}
	if !principal.IsAdmin {
		query += ` AND owner_user_id = ?`
		args = append(args, principal.UserID)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) DeleteAllMessages(ctx context.Context, principal models.SessionPrincipal, filter models.MessageFilter) error {
	selectQuery := `
		SELECT messages.id
		FROM messages
	`
	args := make([]any, 0, 6)
	clauses := make([]string, 0, 3)
	if !principal.IsAdmin {
		clauses = append(clauses, `messages.owner_user_id = ?`)
		args = append(args, principal.UserID)
	}
	if strings.TrimSpace(filter.Query) != "" {
		ftsQuery := buildFTSQuery(filter.Query)
		if ftsQuery != "" {
			selectQuery += `
				JOIN messages_fts ON messages_fts.rowid = messages.id
			`
			clauses = append(clauses, `messages_fts MATCH ?`)
			args = append(args, ftsQuery)
		} else {
			clauses = append(clauses, `(LOWER(messages.subject) LIKE ? OR LOWER(messages.header_from) LIKE ? OR LOWER(messages.header_to) LIKE ?)`)
			term := "%" + strings.ToLower(strings.TrimSpace(filter.Query)) + "%"
			args = append(args, term, term, term)
		}
	}
	if tag := strings.ToLower(strings.TrimSpace(filter.Tag)); tag != "" {
		clauses = append(clauses, `EXISTS (SELECT 1 FROM message_tags WHERE message_tags.message_id = messages.id AND message_tags.tag = ?)`)
		args = append(args, tag)
	}
	if len(clauses) > 0 {
		selectQuery += ` WHERE ` + strings.Join(clauses, ` AND `)
	}

	deleteQuery := `DELETE FROM messages WHERE id IN (` + selectQuery + `)`
	_, err := s.db.ExecContext(ctx, deleteQuery, args...)
	return err
}

func (s *SQLiteStore) Stats(ctx context.Context, principal models.SessionPrincipal) (models.Stats, error) {
	var stats models.Stats
	var latest sql.NullString
	query := `
		SELECT COUNT(*), COALESCE(SUM(size), 0), MAX(received_at)
		FROM messages
	`
	args := []any{}
	if !principal.IsAdmin {
		query += ` WHERE owner_user_id = ?`
		args = append(args, principal.UserID)
	}
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&stats.MessageCount, &stats.TotalSize, &latest); err != nil {
		return models.Stats{}, err
	}
	if latest.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, latest.String)
		if err != nil {
			return models.Stats{}, err
		}
		stats.LatestReceivedAt = &parsed
	}
	return stats, nil
}

func (s *SQLiteStore) listAttachments(ctx context.Context, messageID int64) ([]models.Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, message_id, file_name, content_type, content_id, size, inline
		FROM attachments
		WHERE message_id = ?
		ORDER BY id ASC
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	attachments := make([]models.Attachment, 0)
	for rows.Next() {
		var attachment models.Attachment
		var inlineInt int
		if err := rows.Scan(
			&attachment.ID,
			&attachment.MessageID,
			&attachment.FileName,
			&attachment.ContentType,
			&attachment.ContentID,
			&attachment.Size,
			&inlineInt,
		); err != nil {
			return nil, err
		}
		attachment.Inline = inlineInt == 1
		attachments = append(attachments, attachment)
	}
	return attachments, rows.Err()
}

func scanMessageSummary(scanner interface{ Scan(dest ...any) error }) (models.Message, error) {
	var (
		msg        models.Message
		receivedAt string
		rcptJSON   string
	)
	if err := scanner.Scan(
		&msg.ID,
		&msg.OwnerUserID,
		&receivedAt,
		&msg.MailFrom,
		&rcptJSON,
		&msg.HeaderFrom,
		&msg.HeaderTo,
		&msg.Subject,
		&msg.MessageID,
		&msg.Helo,
		&msg.RemoteIP,
		&msg.Size,
	); err != nil {
		return models.Message{}, err
	}
	if err := hydrateMessage(&msg, receivedAt, rcptJSON, "[]"); err != nil {
		return models.Message{}, err
	}
	return msg, nil
}

func hydrateMessage(msg *models.Message, receivedAt, rcptJSON, headersJSON string) error {
	parsedTime, err := time.Parse(time.RFC3339Nano, receivedAt)
	if err != nil {
		return err
	}
	msg.ReceivedAt = parsedTime
	if err := json.Unmarshal([]byte(rcptJSON), &msg.RcptTo); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(headersJSON), &msg.Headers); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) hydrateTagsForMessages(ctx context.Context, messages []models.Message) error {
	if len(messages) == 0 {
		return nil
	}

	ids := make([]any, 0, len(messages))
	placeholders := make([]string, 0, len(messages))
	indexByID := make(map[int64]int, len(messages))
	for index := range messages {
		messages[index].Tags = nil
		indexByID[messages[index].ID] = index
		ids = append(ids, messages[index].ID)
		placeholders = append(placeholders, "?")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, tag
		FROM message_tags
		WHERE message_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY tag ASC
	`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			messageID int64
			tag       string
		)
		if err := rows.Scan(&messageID, &tag); err != nil {
			return err
		}
		index, ok := indexByID[messageID]
		if !ok {
			continue
		}
		messages[index].Tags = append(messages[index].Tags, tag)
	}
	return rows.Err()
}

func (s *SQLiteStore) listAvailableTags(ctx context.Context, filter models.MessageFilter) ([]string, error) {
	query := `
		SELECT DISTINCT message_tags.tag
		FROM message_tags
		JOIN messages ON messages.id = message_tags.message_id
	`
	args := make([]any, 0, 1)
	clauses := make([]string, 0, 1)
	if !filter.IncludeAll {
		clauses = append(clauses, `messages.owner_user_id = ?`)
		args = append(args, filter.OwnerUserID)
	}
	if len(clauses) > 0 {
		query += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	query += ` ORDER BY message_tags.tag ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tags := make([]string, 0)
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tags, nil
}

func (s *SQLiteStore) backfillMessageTags() error {
	rows, err := s.db.Query(`
		SELECT id, rcpt_to_json
		FROM messages
		WHERE NOT EXISTS (
			SELECT 1 FROM message_tags WHERE message_tags.message_id = messages.id
		)
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type candidate struct {
		id   int64
		tags []string
	}

	candidates := make([]candidate, 0)
	for rows.Next() {
		var (
			id       int64
			rcptJSON string
			rcptTo   []string
		)
		if err := rows.Scan(&id, &rcptJSON); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(rcptJSON), &rcptTo); err != nil {
			return err
		}
		tags := models.ExtractTagsFromRecipients(rcptTo)
		if len(tags) == 0 {
			continue
		}
		candidates = append(candidates, candidate{id: id, tags: tags})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, candidate := range candidates {
		for _, tag := range candidate.tags {
			if _, err := tx.Exec(`
				INSERT OR IGNORE INTO message_tags (message_id, tag)
				VALUES (?, ?)
			`, candidate.id, tag); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func scanUser(scanner interface{ Scan(dest ...any) error }) (models.User, error) {
	var (
		user         models.User
		createdAt    string
		updatedAt    string
		settingsJSON string
	)
	if err := scanner.Scan(&user.ID, &user.Username, &createdAt, &updatedAt, &settingsJSON); err != nil {
		return models.User{}, err
	}
	if err := hydrateUser(&user, createdAt, updatedAt, settingsJSON); err != nil {
		return models.User{}, err
	}
	return user, nil
}

func hydrateUser(user *models.User, createdAt, updatedAt, settingsJSON string) error {
	parsedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return err
	}
	parsedUpdatedAt, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return err
	}
	user.CreatedAt = parsedCreatedAt
	user.UpdatedAt = parsedUpdatedAt
	if err := json.Unmarshal([]byte(settingsJSON), &user.Settings); err != nil {
		return err
	}
	return nil
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func buildFTSQuery(input string) string {
	tokens := ftsTokenPattern.FindAllString(strings.ToLower(input), -1)
	if len(tokens) == 0 {
		return ""
	}

	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		parts = append(parts, fmt.Sprintf(`"%s"*`, token))
	}
	return strings.Join(parts, " AND ")
}

func encodeMessageCursor(receivedAt string, id int64) string {
	raw := receivedAt + "|" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeMessageCursor(cursor string) (string, int64, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", 0, err
	}

	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", 0, errors.New("malformed cursor")
	}

	if _, err := time.Parse(time.RFC3339Nano, parts[0]); err != nil {
		return "", 0, err
	}

	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, err
	}

	return parts[0], id, nil
}
