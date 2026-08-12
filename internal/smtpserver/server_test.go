package smtpserver

import (
	"bufio"
	"strings"
	"testing"
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

func TestReadDataBlockRejectsBareCarriageReturn(t *testing.T) {
	t.Parallel()

	_, err := readDataBlock(bufio.NewReader(strings.NewReader("Subject: bad\rvalue\n.\n")))
	if err == nil || !strings.Contains(err.Error(), "bare carriage return") {
		t.Fatalf("expected bare carriage return error, got %v", err)
	}
}
