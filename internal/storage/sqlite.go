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
	GetMessage(ctx context.Context, id int64) (models.Message, error)
	GetRawMessage(ctx context.Context, id int64) (string, error)
	GetAttachment(ctx context.Context, messageID, attachmentID int64) (models.Attachment, []byte, error)
	DeleteMessage(ctx context.Context, id int64) error
	DeleteAllMessages(ctx context.Context) error
	Stats(ctx context.Context) (models.Stats, error)
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

	if _, err := db.Exec(`INSERT INTO messages_fts(messages_fts) VALUES ('rebuild')`); err != nil {
		return nil, fmt.Errorf("rebuild message search index: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
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

	result, err := tx.ExecContext(ctx, `
		INSERT INTO messages (
			received_at, mail_from, rcpt_to_json, header_from, header_to,
			subject, message_id, helo, remote_ip, size, raw, text_body, html_body, headers_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
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

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return messageID, nil
}

func (s *SQLiteStore) LoadAppSettings(ctx context.Context) (models.AppSettings, bool, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key = ?`, "runtime").Scan(&payload)
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

func (s *SQLiteStore) SaveAppSettings(ctx context.Context, settings models.AppSettings) error {
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
	`, "runtime", string(payload), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) CreateAuthSession(ctx context.Context, session models.AuthSession) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_sessions (session_id, username, csrf_token, expires_at)
		VALUES (?, ?, ?, ?)
	`, session.SessionID, session.Username, session.CSRFToken, session.ExpiresAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) GetAuthSession(ctx context.Context, sessionID string) (models.AuthSession, bool, error) {
	var session models.AuthSession
	var expiresAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, username, csrf_token, expires_at
		FROM auth_sessions
		WHERE session_id = ?
	`, sessionID).Scan(&session.SessionID, &session.Username, &session.CSRFToken, &expiresAt)
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
		SELECT id, received_at, mail_from, rcpt_to_json, header_from, header_to, subject,
		       message_id, helo, remote_ip, size
		FROM messages
	`
	args := make([]any, 0, 6)
	clauses := make([]string, 0, 2)
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

func (s *SQLiteStore) GetMessage(ctx context.Context, id int64) (models.Message, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, received_at, mail_from, rcpt_to_json, header_from, header_to, subject,
		       message_id, helo, remote_ip, size, raw, text_body, html_body, headers_json
		FROM messages WHERE id = ?
	`, id)

	var (
		msg         models.Message
		receivedAt  string
		rcptJSON    string
		headersJSON string
	)
	if err := row.Scan(
		&msg.ID,
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

	attachments, err := s.listAttachments(ctx, msg.ID)
	if err != nil {
		return models.Message{}, err
	}
	msg.Attachments = attachments
	return msg, nil
}

func (s *SQLiteStore) GetRawMessage(ctx context.Context, id int64) (string, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT raw FROM messages WHERE id = ?`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return raw, nil
}

func (s *SQLiteStore) GetAttachment(ctx context.Context, messageID, attachmentID int64) (models.Attachment, []byte, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, message_id, file_name, content_type, content_id, size, inline, content
		FROM attachments
		WHERE message_id = ? AND id = ?
	`, messageID, attachmentID)

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

func (s *SQLiteStore) DeleteMessage(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, id)
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

func (s *SQLiteStore) DeleteAllMessages(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM messages`)
	return err
}

func (s *SQLiteStore) Stats(ctx context.Context) (models.Stats, error) {
	var stats models.Stats
	var latest sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(size), 0), MAX(received_at)
		FROM messages
	`).Scan(&stats.MessageCount, &stats.TotalSize, &latest); err != nil {
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
