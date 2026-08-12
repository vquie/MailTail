package models

import "time"

type Message struct {
	ID          int64        `json:"id"`
	OwnerUserID int64        `json:"ownerUserId,omitempty"`
	ReceivedAt  time.Time    `json:"receivedAt"`
	MailFrom    string       `json:"mailFrom"`
	RcptTo      []string     `json:"rcptTo"`
	Tags        []string     `json:"tags,omitempty"`
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
	OwnerUserID int64
	ExpiresAt   *time.Time
	ReceivedAt  time.Time
	MailFrom    string
	RcptTo      []string
	Tags        []string
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
	Query       string
	Tag         string
	Limit       int
	Cursor      string
	OwnerUserID int64
	IncludeAll  bool
}

type MessagePage struct {
	Messages      []Message `json:"messages"`
	AvailableTags []string  `json:"availableTags,omitempty"`
	NextCursor    string    `json:"nextCursor,omitempty"`
	HasMore       bool      `json:"hasMore"`
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
	AllowedOrigins      string         `json:"allowedOrigins"`
	SMTPLogVerbose      bool           `json:"smtpLogVerbose"`
	MailFailEnabled     bool           `json:"mailFailEnabled"`
	MailFailRules       []MailFailRule `json:"mailFailRules,omitempty"`
	ReportFrom          string         `json:"reportFrom"`
	AllowedRemoteIPs    string         `json:"allowedRemoteIps"`
	AcceptedRcptDomains string         `json:"acceptedRcptDomains"`
	AcceptedFromDomains string         `json:"acceptedFromDomains"`
	AutoDeleteAfterDays int            `json:"autoDeleteAfterDays"`
}

type MailFailRule struct {
	Name                     string `json:"name"`
	Trigger                  string `json:"trigger"`
	Stage                    string `json:"stage"`
	Action                   string `json:"action"`
	AllowAfter               int    `json:"allowAfter,omitempty"`
	MinRetryAfter            string `json:"minRetryAfter,omitempty"`
	ResetAfter               string `json:"resetAfter,omitempty"`
	Code                     int    `json:"code"`
	EnhancedCode             string `json:"enhancedCode,omitempty"`
	FeedbackType             string `json:"feedbackType,omitempty"`
	ReportRecipientLocalPart string `json:"reportRecipientLocalPart,omitempty"`
	Message                  string `json:"message"`
}

type OutboundMessage struct {
	ID           int64
	EnvelopeFrom string
	Recipient    string
	Raw          string
	Attempts     int
	NextAttempt  time.Time
}

type User struct {
	ID        int64       `json:"id"`
	Username  string      `json:"username"`
	Settings  AppSettings `json:"settings"`
	CreatedAt time.Time   `json:"createdAt,omitempty"`
	UpdatedAt time.Time   `json:"updatedAt,omitempty"`
}

type UserCredentials struct {
	User         User
	PasswordHash string
}

type SessionPrincipal struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
	UserID   int64  `json:"userId,omitempty"`
}

type AuthSession struct {
	SessionID string
	Username  string
	UserID    int64
	IsAdmin   bool
	CSRFToken string
	ExpiresAt time.Time
}

type GreylistState struct {
	Key       string
	FirstSeen time.Time
	LastSeen  time.Time
	Attempts  int
}
