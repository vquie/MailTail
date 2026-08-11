package smtpserver

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/parser"
	"github.com/vquie/MailTail/internal/storage"
)

type Server struct {
	addr         string
	store        storage.Store
	parser       *parser.Service
	policy       SMTPResponsePolicy
	logger       *log.Logger
	logVerboseFn func() bool
}

func NewServer(addr string, store storage.Store, parser *parser.Service, policy SMTPResponsePolicy, logger *log.Logger, logVerboseFn func() bool) *Server {
	return &Server{
		addr:         addr,
		store:        store,
		parser:       parser,
		policy:       policy,
		logger:       logger,
		logVerboseFn: logVerboseFn,
	}
}

func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return err
			}
		}
		go s.handleConnection(ctx, conn)
	}
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	remoteIP, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	session := SessionMetadata{RemoteIP: remoteIP}
	if response := s.policy.OnConnect(&session); response != nil {
		s.logSMTPAction(session, "connect-rejected", "", "", response)
		_ = s.sendStatus(conn, response.Code, response.Message)
		return
	}
	s.logSMTPAction(session, "connect-accepted", "", "", nil)
	if err := s.sendLine(conn, "220 MailTail SMTP ready"); err != nil {
		return
	}

	reader := textproto.NewReader(bufio.NewReader(conn))
	for {
		if err := conn.SetDeadline(time.Now().Add(10 * time.Minute)); err != nil {
			return
		}

		line, err := reader.ReadLine()
		if err != nil {
			if err != io.EOF {
				s.logger.Printf("smtp read error: %v", err)
			}
			return
		}

		command, argument := splitCommand(line)
		switch command {
		case "EHLO", "HELO":
			session.Helo = argument
			s.logSMTPAction(session, "helo", "", "", nil)
			if err := s.sendLine(conn, "250-Hello "+argument+"\r\n250 SIZE 10485760"); err != nil {
				return
			}
		case "MAIL":
			from, ok := parsePathArgument(argument, "FROM:")
			if !ok {
				s.logSMTPAction(session, "mailfrom-rejected", "", "", &ResponseError{Code: 501, Message: "Syntax: MAIL FROM:<address>"})
				_ = s.sendStatus(conn, 501, "Syntax: MAIL FROM:<address>")
				continue
			}
			if response := s.policy.OnMailFrom(&session, from); response != nil {
				s.logSMTPAction(session, "mailfrom-rejected", from, "", response)
				_ = s.sendStatus(conn, response.Code, response.Message)
				continue
			}
			session.RcptTo = nil
			session.OwnerUserID = 0
			s.logSMTPAction(session, "mailfrom-accepted", from, "", nil)
			if err := s.sendStatus(conn, 250, "Sender OK"); err != nil {
				return
			}
		case "RCPT":
			recipient, ok := parsePathArgument(argument, "TO:")
			if !ok {
				s.logSMTPAction(session, "rcpt-rejected", "", "", &ResponseError{Code: 501, Message: "Syntax: RCPT TO:<address>"})
				_ = s.sendStatus(conn, 501, "Syntax: RCPT TO:<address>")
				continue
			}
			if response := s.policy.OnRcptTo(&session, recipient); response != nil {
				s.logSMTPAction(session, "rcpt-rejected", "", recipient, response)
				_ = s.sendStatus(conn, response.Code, response.Message)
				continue
			}
			session.RcptTo = append(session.RcptTo, recipient)
			s.logSMTPAction(session, "rcpt-accepted", "", recipient, nil)
			if err := s.sendStatus(conn, 250, "Recipient OK"); err != nil {
				return
			}
		case "DATA":
			if session.MailFrom == "" || len(session.RcptTo) == 0 {
				s.logSMTPAction(session, "data-rejected", "", "", &ResponseError{Code: 503, Message: "Need MAIL FROM and RCPT TO first"})
				_ = s.sendStatus(conn, 503, "Need MAIL FROM and RCPT TO first")
				continue
			}
			if response := s.policy.OnData(&session); response != nil {
				s.logSMTPAction(session, "data-rejected", "", "", response)
				_ = s.sendStatus(conn, response.Code, response.Message)
				continue
			}
			if err := s.sendStatus(conn, 354, "End data with <CR><LF>.<CR><LF>"); err != nil {
				return
			}
			raw, err := readDataBlock(reader.R)
			if err != nil {
				s.logSMTPAction(session, "data-read-failed", "", "", &ResponseError{Code: 451, Message: "Failed to read message data"})
				_ = s.sendStatus(conn, 451, "Failed to read message data")
				return
			}
			if err := s.persistMessage(ctx, session, raw); err != nil {
				s.logSMTPAction(session, "store-failed", "", "", err)
				_ = s.sendStatus(conn, 451, "Failed to store message")
				continue
			}
			s.queueReports(ctx, session, raw)
			s.logSMTPAction(session, "message-accepted", "", "", nil)
			if err := s.sendStatus(conn, 250, "Message accepted"); err != nil {
				return
			}
		case "RSET":
			session.MailFrom = ""
			session.RcptTo = nil
			s.logSMTPAction(session, "reset", "", "", nil)
			if err := s.sendStatus(conn, 250, "State reset"); err != nil {
				return
			}
		case "NOOP":
			if err := s.sendStatus(conn, 250, "OK"); err != nil {
				return
			}
		case "QUIT":
			s.logSMTPAction(session, "quit", "", "", nil)
			_ = s.sendStatus(conn, 221, "Bye")
			return
		default:
			s.logSMTPAction(session, "command-rejected", "", "", &ResponseError{Code: 502, Message: "Command not implemented"})
			if err := s.sendStatus(conn, 502, "Command not implemented"); err != nil {
				return
			}
		}
	}
}

func (s *Server) queueReports(ctx context.Context, session SessionMetadata, raw []byte) {
	policy, ok := s.policy.(MessageReportPolicy)
	if !ok || hasAutoSubmittedHeader(raw) {
		return
	}
	requests, err := policy.ReportsFor(&session)
	if err != nil {
		s.logger.Printf("report policy failed: %v", err)
		return
	}
	for _, request := range requests {
		message, err := BuildReportMessage(request, session, raw, time.Now().UTC())
		if err != nil {
			s.logger.Printf("report generation failed action=%q: %v", request.Action, err)
			continue
		}
		if err := s.store.EnqueueOutboundMessage(ctx, message); err != nil {
			s.logger.Printf("report enqueue failed action=%q: %v", request.Action, err)
			continue
		}
		s.logger.Printf("report queued action=%q recipient=%q", request.Action, sanitizeLogValue(message.Recipient))
	}
}

func hasAutoSubmittedHeader(raw []byte) bool {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	value := strings.TrimSpace(message.Header.Get("Auto-Submitted"))
	return value != "" && !strings.EqualFold(value, "no")
}

func (s *Server) persistMessage(ctx context.Context, session SessionMetadata, raw []byte) error {
	parsed, err := s.parser.Parse(raw)
	if err != nil {
		return err
	}
	parsed.ReceivedAt = time.Now().UTC()
	parsed.MailFrom = session.MailFrom
	parsed.RcptTo = append([]string(nil), session.RcptTo...)
	parsed.Tags = models.ExtractTagsFromRecipients(parsed.RcptTo)
	parsed.Helo = session.Helo
	parsed.RemoteIP = session.RemoteIP
	if parsed.HeaderTo == "" {
		parsed.HeaderTo = strings.Join(session.RcptTo, ", ")
	}
	if parsed.HeaderFrom == "" {
		parsed.HeaderFrom = session.MailFrom
	}
	if parsed.Subject == "" {
		parsed.Subject = "(no subject)"
	}
	parsed.OwnerUserID = session.OwnerUserID
	switch {
	case session.OwnerUserID > 0:
		settings, ok, err := s.store.LoadUserSettings(ctx, session.OwnerUserID)
		if err != nil {
			return err
		}
		if ok && settings.AutoDeleteAfterDays > 0 {
			expiresAt := parsed.ReceivedAt.Add(time.Duration(settings.AutoDeleteAfterDays) * 24 * time.Hour)
			parsed.ExpiresAt = &expiresAt
		}
	case session.OwnerUserID == 0:
		settings, ok, err := s.store.LoadAdminMailboxSettings(ctx)
		if err != nil {
			return err
		}
		if ok && settings.AutoDeleteAfterDays > 0 {
			expiresAt := parsed.ReceivedAt.Add(time.Duration(settings.AutoDeleteAfterDays) * 24 * time.Hour)
			parsed.ExpiresAt = &expiresAt
		}
	}
	if _, err := s.store.CreateMessage(ctx, parsed); err != nil {
		s.logger.Printf("message create failed: %v", err)
		return err
	}
	return nil
}

func splitCommand(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}
	parts := strings.SplitN(line, " ", 2)
	command := strings.ToUpper(parts[0])
	if len(parts) == 1 {
		return command, ""
	}
	return command, strings.TrimSpace(parts[1])
}

func parsePathArgument(argument, prefix string) (string, bool) {
	argument = strings.TrimSpace(argument)
	if !strings.HasPrefix(strings.ToUpper(argument), prefix) {
		return "", false
	}
	value := strings.TrimSpace(argument[len(prefix):])
	if strings.HasPrefix(value, "<") {
		end := strings.Index(value, ">")
		if end == -1 {
			return "", false
		}
		return strings.TrimSpace(value[1:end]), true
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", false
	}
	return strings.TrimSpace(fields[0]), true
}

func readDataBlock(reader *bufio.Reader) ([]byte, error) {
	var buffer bytes.Buffer
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if line == ".\r\n" || line == ".\n" {
			break
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		buffer.WriteString(line)
	}
	return buffer.Bytes(), nil
}

func (s *Server) sendStatus(conn net.Conn, code int, message string) error {
	return s.sendLine(conn, fmt.Sprintf("%d %s", code, message))
}

func (s *Server) sendLine(conn net.Conn, value string) error {
	_, err := io.WriteString(conn, value+"\r\n")
	return err
}

func (s *Server) logSMTPAction(session SessionMetadata, action, sender, recipient string, err error) {
	if !s.logVerbose() && !shouldLogSMTPAction(action, err) {
		return
	}

	if sender == "" {
		sender = session.MailFrom
	}
	if recipient == "" && len(session.RcptTo) > 0 {
		recipient = strings.Join(session.RcptTo, ",")
	}

	message := "-"
	if err != nil {
		message = err.Error()
	}

	s.logger.Printf(
		`smtp remote_ip=%q helo=%q sender=%q recipient=%q action=%q error=%q`,
		sanitizeLogValue(session.RemoteIP),
		sanitizeLogValue(session.Helo),
		sanitizeLogValue(sender),
		sanitizeLogValue(recipient),
		sanitizeLogValue(action),
		sanitizeLogValue(message),
	)
}

func (s *Server) logVerbose() bool {
	if s.logVerboseFn == nil {
		return false
	}
	return s.logVerboseFn()
}

func shouldLogSMTPAction(action string, err error) bool {
	if err != nil {
		return true
	}

	switch action {
	case "message-accepted":
		return true
	default:
		return false
	}
}

func sanitizeLogValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}

	replacer := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ")
	value = replacer.Replace(value)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 256 {
		return value[:253] + "..."
	}
	return value
}
