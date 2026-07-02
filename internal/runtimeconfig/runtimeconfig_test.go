package runtimeconfig

import (
	"reflect"
	"testing"

	"github.com/vquie/MailTail/internal/models"
)

func TestCSVListSplitsCommaAndNewlineSeparatedValues(t *testing.T) {
	t.Parallel()

	value := "alpha.test,\nbeta.test\r\ngamma.test\n delta.test "

	got := CSVList(value)
	want := []string{"alpha.test", "beta.test", "gamma.test", "delta.test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected CSVList result: got %v want %v", got, want)
	}
}

func TestNormalizeUserSettingsNormalizesNewlineSeparatedLists(t *testing.T) {
	t.Parallel()

	settings := NormalizeUserSettings(models.AppSettings{
		AllowedRemoteIPs:     "127.0.0.1\n10.0.0.0/8",
		AcceptedRcptDomains:  "alpha.test,\nbeta.test",
		AcceptedFromDomains:  "sender.test\r\nrelay.test",
		AutoDeleteAfterDays:  7,
	})

	if settings.AllowedRemoteIPs != "127.0.0.1,10.0.0.0/8" {
		t.Fatalf("unexpected AllowedRemoteIPs: %q", settings.AllowedRemoteIPs)
	}
	if settings.AcceptedRcptDomains != "alpha.test,beta.test" {
		t.Fatalf("unexpected AcceptedRcptDomains: %q", settings.AcceptedRcptDomains)
	}
	if settings.AcceptedFromDomains != "sender.test,relay.test" {
		t.Fatalf("unexpected AcceptedFromDomains: %q", settings.AcceptedFromDomains)
	}
}
