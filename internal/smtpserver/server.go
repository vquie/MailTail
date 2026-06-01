package smtpserver

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/textproto"
	"strings"
	"time"

	"github.com/vquie/MailTail/internal/parser"
	"github.com/vquie/MailTail/internal/storage"
)

type Server struct {
	addr   string
	store  storage.Store
	parser *parser.Service
	policy SMTPResponsePolicy
	logger *log.Logger
}

func NewServer(addr string, store storage.Store, parser *parser.Service, policy SMTPResponsePolicy, logger *log.Logger) *Server {
	return &Server{
		addr:   addr,
		store:  store,
		parser: parser,
		policy: policy,
		logger: logger,
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
	if err := s.sendLine(conn, "220 MailTail SMTP ready"); err != nil {
		return
	}
	if response := s.policy.OnConnect(session); response != nil {
		_ = s.sendStatus(conn, response.Code, response.Message)
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
			if err := s.sendLine(conn, "250-Hello "+argument+"\r\n250 SIZE 10485760"); err != nil {
				return
			}
		case "MAIL":
			from, ok := parsePathArgument(argument, "FROM:")
			if !ok {
				_ = s.sendStatus(conn, 501, "Syntax: MAIL FROM:<address>")
				continue
			}
			if response := s.policy.OnMailFrom(session, from); response != nil {
				_ = s.sendStatus(conn, response.Code, response.Message)
				continue
			}
			session.MailFrom = from
			session.RcptTo = nil
			if err := s.sendStatus(conn, 250, "Sender OK"); err != nil {
				return
			}
		case "RCPT":
			recipient, ok := parsePathArgument(argument, "TO:")
			if !ok {
				_ = s.sendStatus(conn, 501, "Syntax: RCPT TO:<address>")
				continue
			}
			if response := s.policy.OnRcptTo(session, recipient); response != nil {
				_ = s.sendStatus(conn, response.Code, response.Message)
				continue
			}
			session.RcptTo = append(session.RcptTo, recipient)
			if err := s.sendStatus(conn, 250, "Recipient OK"); err != nil {
				return
			}
		case "DATA":
			if session.MailFrom == "" || len(session.RcptTo) == 0 {
				_ = s.sendStatus(conn, 503, "Need MAIL FROM and RCPT TO first")
				continue
			}
			if response := s.policy.OnData(session); response != nil {
				_ = s.sendStatus(conn, response.Code, response.Message)
				continue
			}
			if err := s.sendStatus(conn, 354, "End data with <CR><LF>.<CR><LF>"); err != nil {
				return
			}
			raw, err := readDataBlock(reader.R)
			if err != nil {
				_ = s.sendStatus(conn, 451, "Failed to read message data")
				return
			}
			if err := s.persistMessage(ctx, session, raw); err != nil {
				s.logger.Printf("store message: %v", err)
				_ = s.sendStatus(conn, 451, "Failed to store message")
				continue
			}
			if err := s.sendStatus(conn, 250, "Message accepted"); err != nil {
				return
			}
		case "RSET":
			session.MailFrom = ""
			session.RcptTo = nil
			if err := s.sendStatus(conn, 250, "State reset"); err != nil {
				return
			}
		case "NOOP":
			if err := s.sendStatus(conn, 250, "OK"); err != nil {
				return
			}
		case "QUIT":
			_ = s.sendStatus(conn, 221, "Bye")
			return
		default:
			if err := s.sendStatus(conn, 502, "Command not implemented"); err != nil {
				return
			}
		}
	}
}

func (s *Server) persistMessage(ctx context.Context, session SessionMetadata, raw []byte) error {
	parsed, err := s.parser.Parse(raw)
	if err != nil {
		return err
	}
	parsed.ReceivedAt = time.Now().UTC()
	parsed.MailFrom = session.MailFrom
	parsed.RcptTo = append([]string(nil), session.RcptTo...)
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
