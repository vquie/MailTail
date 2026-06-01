package parser

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"

	"github.com/vquie/MailTail/internal/models"
)

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Parse(raw []byte) (models.StoredMessage, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return models.StoredMessage{}, err
	}

	headers := make([]models.Header, 0, len(msg.Header))
	for key, values := range msg.Header {
		headers = append(headers, models.Header{Key: key, Value: strings.Join(values, ", ")})
	}

	stored := models.StoredMessage{
		HeaderFrom: msg.Header.Get("From"),
		HeaderTo:   msg.Header.Get("To"),
		Subject:    msg.Header.Get("Subject"),
		MessageID:  msg.Header.Get("Message-ID"),
		Raw:        string(raw),
		Headers:    headers,
		Size:       len(raw),
	}

	contentType := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		body, readErr := io.ReadAll(msg.Body)
		if readErr != nil {
			return models.StoredMessage{}, readErr
		}
		stored.TextBody = string(body)
		return stored, nil
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		if err := s.walkMultipart(multipart.NewReader(msg.Body, params["boundary"]), &stored); err != nil {
			return models.StoredMessage{}, err
		}
		return stored, nil
	}

	body, err := io.ReadAll(decodeTransferEncoding(msg.Header.Get("Content-Transfer-Encoding"), msg.Body))
	if err != nil {
		return models.StoredMessage{}, err
	}
	if mediaType == "text/html" {
		stored.HTMLBody = string(body)
	} else {
		stored.TextBody = string(body)
	}

	return stored, nil
}

func (s *Service) ParseHeaders(raw string) ([]models.Header, error) {
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	headers := make([]models.Header, 0, len(msg.Header))
	for key, values := range msg.Header {
		headers = append(headers, models.Header{Key: key, Value: strings.Join(values, ", ")})
	}
	return headers, nil
}

func (s *Service) walkMultipart(reader *multipart.Reader, stored *models.StoredMessage) error {
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		contentType := part.Header.Get("Content-Type")
		mediaType, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			mediaType = "application/octet-stream"
		}

		if strings.HasPrefix(mediaType, "multipart/") {
			if err := s.walkMultipart(multipart.NewReader(part, params["boundary"]), stored); err != nil {
				return err
			}
			continue
		}

		content, err := io.ReadAll(decodeTransferEncoding(part.Header.Get("Content-Transfer-Encoding"), part))
		if err != nil {
			return err
		}

		disposition, dispParams, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		fileName := dispParams["filename"]
		contentID := strings.Trim(part.Header.Get("Content-ID"), "<>")
		inline := disposition == "inline"

		switch {
		case mediaType == "text/plain" && fileName == "" && stored.TextBody == "":
			stored.TextBody = string(content)
		case mediaType == "text/html" && fileName == "" && stored.HTMLBody == "":
			stored.HTMLBody = string(content)
		default:
			if fileName == "" {
				fileName = part.FileName()
			}
			if fileName == "" {
				fileName = "attachment"
			}
			stored.Attachments = append(stored.Attachments, models.StoredAttachment{
				FileName:    fileName,
				ContentType: mediaType,
				ContentID:   contentID,
				Size:        len(content),
				Inline:      inline,
				Content:     content,
			})
		}
	}
}

func decodeTransferEncoding(encoding string, reader io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		return quotedprintable.NewReader(reader)
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, reader)
	default:
		return reader
	}
}
