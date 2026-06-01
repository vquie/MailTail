CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    received_at TEXT NOT NULL,
    mail_from TEXT NOT NULL,
    rcpt_to_json TEXT NOT NULL,
    header_from TEXT NOT NULL,
    header_to TEXT NOT NULL,
    subject TEXT NOT NULL,
    message_id TEXT NOT NULL,
    helo TEXT NOT NULL,
    remote_ip TEXT NOT NULL,
    size INTEGER NOT NULL,
    raw TEXT NOT NULL,
    text_body TEXT NOT NULL,
    html_body TEXT NOT NULL,
    headers_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS attachments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    file_name TEXT NOT NULL,
    content_type TEXT NOT NULL,
    content_id TEXT NOT NULL,
    size INTEGER NOT NULL,
    inline INTEGER NOT NULL,
    content BLOB NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_received_at ON messages(received_at DESC);
CREATE INDEX IF NOT EXISTS idx_messages_subject ON messages(subject);
CREATE INDEX IF NOT EXISTS idx_messages_header_from ON messages(header_from);
CREATE INDEX IF NOT EXISTS idx_messages_header_to ON messages(header_to);
CREATE INDEX IF NOT EXISTS idx_attachments_message_id ON attachments(message_id);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    subject,
    header_from,
    header_to,
    content='messages',
    content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, subject, header_from, header_to)
    VALUES (new.id, new.subject, new.header_from, new.header_to);
END;

CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, subject, header_from, header_to)
    VALUES ('delete', old.id, old.subject, old.header_from, old.header_to);
END;

CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, subject, header_from, header_to)
    VALUES ('delete', old.id, old.subject, old.header_from, old.header_to);
    INSERT INTO messages_fts(rowid, subject, header_from, header_to)
    VALUES (new.id, new.subject, new.header_from, new.header_to);
END;
