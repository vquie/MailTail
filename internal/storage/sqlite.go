package storage

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/vitaliquiering/mailtail/internal/models"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var ErrNotFound = errors.New("not found")
var ftsTokenPattern = regexp.MustCompile(`[[:alnum:]_.@+-]+`)

type Store interface {
	CreateMessage(ctx context.Context, message models.StoredMessage) (int64, error)
	ListMessages(ctx context.Context, filter models.MessageFilter) ([]models.Message, error)
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

func (s *SQLiteStore) ListMessages(ctx context.Context, filter models.MessageFilter) ([]models.Message, error) {
	query := `
		SELECT id, received_at, mail_from, rcpt_to_json, header_from, header_to, subject,
		       message_id, helo, remote_ip, size
		FROM messages
	`
	args := make([]any, 0, 3)
	if strings.TrimSpace(filter.Query) != "" {
		ftsQuery := buildFTSQuery(filter.Query)
		if ftsQuery != "" {
			query += `
				JOIN messages_fts ON messages_fts.rowid = messages.id
				WHERE messages_fts MATCH ?
			`
			args = append(args, ftsQuery)
		} else {
			query += ` WHERE LOWER(subject) LIKE ? OR LOWER(header_from) LIKE ? OR LOWER(header_to) LIKE ?`
			term := "%" + strings.ToLower(strings.TrimSpace(filter.Query)) + "%"
			args = append(args, term, term, term)
		}
	}
	query += ` ORDER BY received_at DESC`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]models.Message, 0)
	for rows.Next() {
		msg, err := scanMessageSummary(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
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
