package models

import (
	"reflect"
	"testing"
)

func TestExtractTagsFromRecipients(t *testing.T) {
	t.Parallel()

	recipients := []string{
		"user+Alpha@example.test",
		"other@example.test",
		"user+beta+release@example.test",
		"user+alpha@example.test",
		" malformed ",
	}

	got := ExtractTagsFromRecipients(recipients)
	want := []string{"alpha", "beta+release"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tags: got %v want %v", got, want)
	}
}

func TestExtractTagFromRecipient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		recipient string
		wantTag   string
		wantOK    bool
	}{
		{name: "simple", recipient: "user+alpha@example.test", wantTag: "alpha", wantOK: true},
		{name: "compound", recipient: "user+alpha+beta@example.test", wantTag: "alpha+beta", wantOK: true},
		{name: "no plus", recipient: "user@example.test", wantOK: false},
		{name: "empty suffix", recipient: "user+@example.test", wantOK: false},
		{name: "missing at", recipient: "user+alpha", wantOK: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gotTag, gotOK := ExtractTagFromRecipient(test.recipient)
			if gotTag != test.wantTag || gotOK != test.wantOK {
				t.Fatalf("unexpected result for %q: got (%q, %v) want (%q, %v)", test.recipient, gotTag, gotOK, test.wantTag, test.wantOK)
			}
		})
	}
}
