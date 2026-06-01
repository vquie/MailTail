package smtpserver

type SMTPResponsePolicy interface {
	OnConnect(session SessionMetadata) *ResponseError
	OnMailFrom(session SessionMetadata, from string) *ResponseError
	OnRcptTo(session SessionMetadata, recipient string) *ResponseError
	OnData(session SessionMetadata) *ResponseError
}

type SessionMetadata struct {
	Helo     string
	RemoteIP string
	MailFrom string
	RcptTo   []string
}

type ResponseError struct {
	Code    int
	Message string
}

func (e *ResponseError) Error() string {
	return e.Message
}

type AcceptAllPolicy struct{}

func NewAcceptAllPolicy() *AcceptAllPolicy {
	return &AcceptAllPolicy{}
}

func (p *AcceptAllPolicy) OnConnect(SessionMetadata) *ResponseError          { return nil }
func (p *AcceptAllPolicy) OnMailFrom(SessionMetadata, string) *ResponseError { return nil }
func (p *AcceptAllPolicy) OnRcptTo(SessionMetadata, string) *ResponseError   { return nil }
func (p *AcceptAllPolicy) OnData(SessionMetadata) *ResponseError             { return nil }
