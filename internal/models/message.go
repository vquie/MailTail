package models

import "time"

type Message struct {
	ID          int64        `json:"id"`
	ReceivedAt  time.Time    `json:"receivedAt"`
	MailFrom    string       `json:"mailFrom"`
	RcptTo      []string     `json:"rcptTo"`
	HeaderFrom  string       `json:"headerFrom"`
	HeaderTo    string       `json:"headerTo"`
	Subject     string       `json:"subject"`
	MessageID   string       `json:"messageId"`
	Helo        string       `json:"helo"`
	RemoteIP    string       `json:"remoteIp"`
	Size        int          `json:"size"`
	Raw         string       `json:"raw,omitempty"`
	TextBody    string       `json:"textBody,omitempty"`
	HTMLBody    string       `json:"htmlBody,omitempty"`
	Headers     []Header     `json:"headers,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Attachment struct {
	ID          int64  `json:"id"`
	MessageID   int64  `json:"messageId"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	ContentID   string `json:"contentId"`
	Size        int    `json:"size"`
	Inline      bool   `json:"inline"`
}

type StoredMessage struct {
	ReceivedAt  time.Time
	MailFrom    string
	RcptTo      []string
	HeaderFrom  string
	HeaderTo    string
	Subject     string
	MessageID   string
	Helo        string
	RemoteIP    string
	Size        int
	Raw         string
	TextBody    string
	HTMLBody    string
	Headers     []Header
	Attachments []StoredAttachment
}

type StoredAttachment struct {
	FileName    string
	ContentType string
	ContentID   string
	Size        int
	Inline      bool
	Content     []byte
}

type MessageFilter struct {
	Query  string
	Limit  int
	Cursor string
}

type MessagePage struct {
	Messages   []Message `json:"messages"`
	NextCursor string    `json:"nextCursor,omitempty"`
	HasMore    bool      `json:"hasMore"`
}

type Stats struct {
	MessageCount     int64      `json:"messageCount"`
	TotalSize        int64      `json:"totalSize"`
	LatestReceivedAt *time.Time `json:"latestReceivedAt,omitempty"`
}

type AppInfo struct {
	Version string `json:"version"`
}

type AppSettings struct {
	AllowedOrigins      string `json:"allowedOrigins"`
	SMTPLogVerbose      bool   `json:"smtpLogVerbose"`
	MailFailEnabled     bool   `json:"mailFailEnabled"`
	MailFailRulesFile   string `json:"mailFailRulesFile"`
	AllowedRemoteIPs    string `json:"allowedRemoteIps"`
	AcceptedRcptDomains string `json:"acceptedRcptDomains"`
	AcceptedFromDomains string `json:"acceptedFromDomains"`
	AutoDeleteAfterDays int    `json:"autoDeleteAfterDays"`
}

type AuthSession struct {
	SessionID string
	Username  string
	CSRFToken string
	ExpiresAt time.Time
}

type GreylistState struct {
	Key       string
	FirstSeen time.Time
	LastSeen  time.Time
	Attempts  int
}
