package models

import "strings"

func ExtractTagsFromRecipients(recipients []string) []string {
	tags := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		tag, ok := ExtractTagFromRecipient(recipient)
		if !ok {
			continue
		}
		tags = append(tags, tag)
	}
	return NormalizeTags(tags)
}

func ExtractTagFromRecipient(recipient string) (string, bool) {
	recipient = strings.TrimSpace(recipient)
	at := strings.LastIndex(recipient, "@")
	if at <= 0 {
		return "", false
	}

	localPart := strings.TrimSpace(recipient[:at])
	plus := strings.Index(localPart, "+")
	if plus < 0 || plus == len(localPart)-1 {
		return "", false
	}

	tag := strings.ToLower(strings.TrimSpace(localPart[plus+1:]))
	if tag == "" {
		return "", false
	}
	return tag, true
}

func NormalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(tags))
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}

	if len(normalized) == 0 {
		return nil
	}
	return normalized
}
