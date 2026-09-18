package smtpserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/vquie/MailTail/internal/models"
	"github.com/vquie/MailTail/internal/parser"
)

func TestReadDataBlockCanonicalizesCRLFAndDotTransparency(t *testing.T) {
	t.Parallel()

	raw, err := readDataBlock(bufio.NewReader(strings.NewReader("Subject: test\n\n..leading dot\n.\n")))
	if err != nil {
		t.Fatalf("read DATA: %v", err)
	}
	want := "Subject: test\r\n\r\n.leading dot\r\n"
	if string(raw) != want {
		t.Fatalf("unexpected canonical DATA:\n got %q\nwant %q", raw, want)
	}
}

func TestReadDataBlockEnforcesMaximumSizeAndDrainsInput(t *testing.T) {
	t.Parallel()

	reader := bufio.NewReader(strings.NewReader("12345\n67890\n.\nNEXT\n"))
	_, err := readDataBlockLimit(reader, 8)
	if !errors.Is(err, errMessageTooLarge) {
		t.Fatalf("expected size error, got %v", err)
	}
	next, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read after oversized DATA: %v", err)
	}
	if next != "NEXT\n" {
		t.Fatalf("DATA was not drained, got %q", next)
	}
}

func TestReadDataBlockAcceptsMessageAtExactLimit(t *testing.T) {
	t.Parallel()

	raw, err := readDataBlockLimit(bufio.NewReader(strings.NewReader("12345\n.\n")), 7)
	if err != nil {
		t.Fatalf("read DATA: %v", err)
	}
	if string(raw) != "12345\r\n" {
		t.Fatalf("unexpected DATA: %q", raw)
	}
}

func TestReadDataBlockRejectsOverlongLineWithoutLargeAllocation(t *testing.T) {
	t.Parallel()

	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", maxSMTPDataLineSize+200)+"\n.\nNEXT\n"), 64)
	_, err := readDataBlockLimit(reader, maxMessageSize)
	if !errors.Is(err, errDataLineTooLong) {
		t.Fatalf("expected overlong-line error, got %v", err)
	}
	next, err := reader.ReadString('\n')
	if err != nil || next != "NEXT\n" {
		t.Fatalf("message was not drained: next=%q err=%v", next, err)
	}
}

func TestParseDeclaredMessageSize(t *testing.T) {
	t.Parallel()

	size, present, err := parseDeclaredMessageSize("FROM:<sender@example.test> SIZE=10485760")
	if err != nil || !present || size != maxMessageSize {
		t.Fatalf("unexpected SIZE parse: size=%d present=%v err=%v", size, present, err)
	}
	if _, present, err := parseDeclaredMessageSize("FROM:<sender@example.test>"); err != nil || present {
		t.Fatalf("unexpected absent SIZE result: present=%v err=%v", present, err)
	}
	if _, _, err := parseDeclaredMessageSize("FROM:<sender@example.test> SIZE=invalid"); err == nil {
		t.Fatal("invalid SIZE was accepted")
	}
}

func TestReadDataBlockRejectsBareCarriageReturn(t *testing.T) {
	t.Parallel()

	_, err := readDataBlock(bufio.NewReader(strings.NewReader("Subject: bad\rvalue\n.\n")))
	if err == nil || !strings.Contains(err.Error(), "bare carriage return") {
		t.Fatalf("expected bare carriage return error, got %v", err)
	}
}

func TestSMTPServerPersistsMessageForResolvedUserOnly(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	userA, err := store.CreateUser(t.Context(), "user-a", "hash", models.AppSettings{AcceptedRcptDomains: "a.test"})
	if err != nil {
		t.Fatalf("create user A: %v", err)
	}
	userB, err := store.CreateUser(t.Context(), "user-b", "hash", models.AppSettings{AcceptedRcptDomains: "b.test"})
	if err != nil {
		t.Fatalf("create user B: %v", err)
	}
	policy, err := NewDomainPolicy(DomainPolicyConfig{}, store)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	server := NewServer(":0", store, parser.NewService(), policy, log.New(io.Discard, "", 0), func() bool { return false })
	client, serverConn := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	go server.handleConnection(context.Background(), serverConn)
	reader := bufio.NewReader(client)
	readCode := func(want int) {
		t.Helper()
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read SMTP response: %v", readErr)
		}
		if !strings.HasPrefix(line, fmt.Sprintf("%d ", want)) {
			t.Fatalf("unexpected SMTP response: got %q want %d", line, want)
		}
	}
	write := func(value string) {
		t.Helper()
		if _, writeErr := io.WriteString(client, value+"\r\n"); writeErr != nil {
			t.Fatalf("write SMTP command: %v", writeErr)
		}
	}

	readCode(220)
	write("MAIL FROM:<sender@outside.test>")
	readCode(250)
	write("RCPT TO:<inbox@a.test>")
	readCode(250)
	write("DATA")
	readCode(354)
	write("From: sender@outside.test\r\nTo: inbox@a.test\r\nSubject: isolated\r\n\r\nhello\r\n.")
	readCode(250)
	write("QUIT")
	readCode(221)

	pageA, err := store.ListMessages(t.Context(), models.MessageFilter{OwnerUserID: userA.ID, Limit: 25})
	if err != nil {
		t.Fatalf("list user A inbox: %v", err)
	}
	pageB, err := store.ListMessages(t.Context(), models.MessageFilter{OwnerUserID: userB.ID, Limit: 25})
	if err != nil {
		t.Fatalf("list user B inbox: %v", err)
	}
	if len(pageA.Messages) != 1 || pageA.Messages[0].Subject != "isolated" {
		t.Fatalf("message missing from owner inbox: %+v", pageA.Messages)
	}
	if len(pageB.Messages) != 0 {
		t.Fatalf("message leaked to another inbox: %+v", pageB.Messages)
	}
}
