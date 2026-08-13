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
		AllowedRemoteIPs:    "127.0.0.1\n10.0.0.0/8",
		AcceptedRcptDomains: "alpha.test,\nbeta.test",
		AcceptedFromDomains: "sender.test\r\nrelay.test",
		AutoDeleteAfterDays: 7,
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

func TestNormalizeUserSettingsMakesARFStageAndTextImplicit(t *testing.T) {
	t.Parallel()

	settings := NormalizeUserSettings(models.AppSettings{
		MailFailRules: []models.MailFailRule{{
			Name:                     "ARF",
			Trigger:                  "mf-arf",
			Stage:                    "mailfrom",
			Action:                   "arf",
			FeedbackType:             " Fraud ",
			ReportRecipientLocalPart: " fbl ",
			Message:                  "user-supplied report text",
		}},
	})

	if len(settings.MailFailRules) != 1 {
		t.Fatalf("unexpected normalized rules: %#v", settings.MailFailRules)
	}
	rule := settings.MailFailRules[0]
	if rule.Stage != "data" {
		t.Fatalf("report stage must be normalized to data, got %q", rule.Stage)
	}
	if rule.Message != "" {
		t.Fatalf("report text must not be stored as user configuration, got %q", rule.Message)
	}
	if rule.FeedbackType != "fraud" {
		t.Fatalf("unexpected normalized feedback type: %q", rule.FeedbackType)
	}
	if rule.ReportRecipientLocalPart != "fbl" {
		t.Fatalf("unexpected normalized report recipient local part: %q", rule.ReportRecipientLocalPart)
	}
}

func TestNormalizeUserSettingsPreservesOriginalReportRecipientLocalPart(t *testing.T) {
	t.Parallel()

	settings := NormalizeUserSettings(models.AppSettings{
		MailFailRules: []models.MailFailRule{{
			Name:                     "original",
			Trigger:                  "mf-original",
			Action:                   "original-report",
			ReportRecipientLocalPart: " reports ",
		}},
	})

	rule := settings.MailFailRules[0]
	if rule.Stage != "data" || rule.ReportRecipientLocalPart != "reports" {
		t.Fatalf("unexpected normalized original report: %#v", rule)
	}
}

func TestNormalizeUserSettingsPreservesAsyncBounceText(t *testing.T) {
	t.Parallel()

	settings := NormalizeUserSettings(models.AppSettings{
		MailFailRules: []models.MailFailRule{{
			Name:                     "quota bounce",
			Trigger:                  "mf-quota-bounce",
			Action:                   "async-bounce",
			FeedbackType:             "abuse",
			ReportRecipientLocalPart: "fbl",
			Message:                  " Mailbox quota exceeded ",
		}},
	})

	rule := settings.MailFailRules[0]
	if rule.Stage != "data" || rule.Message != "Mailbox quota exceeded" {
		t.Fatalf("unexpected normalized async bounce: %#v", rule)
	}
	if rule.FeedbackType != "" {
		t.Fatalf("async bounce must not store an ARF feedback type, got %q", rule.FeedbackType)
	}
	if rule.ReportRecipientLocalPart != "" {
		t.Fatalf("async bounce must not store a report recipient local part, got %q", rule.ReportRecipientLocalPart)
	}
}
