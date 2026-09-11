package util

import (
	"fmt"
	"strings"
	"uuid"
)

// ParseUUID trims whitespace and validates value as an RFC 4122 UUID,
// returning its canonical (lowercase, hyphenated) string form. This is the
// single source of truth for UUID format validation across the backend.
func ParseUUID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("invalid uuid value")
	}
	return parsed.String(), nil
}

// ValidateUUIDs checks that values is non-empty and that every entry is a
// valid UUID. Use for bulk-action request bodies where an empty list is
// itself a client error (e.g. "select at least one item").
func ValidateUUIDs(values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("uuids cannot be empty")
	}
	return ValidateUUIDsAllowEmpty(values)
}

// ValidateUUIDsAllowEmpty checks that every entry in values is a valid
// UUID, without requiring the list itself to be non-empty. Use for
// optional filter/list request fields.
func ValidateUUIDsAllowEmpty(values []string) error {
	for _, value := range values {
		if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("invalid uuid value")
		}
	}
	return nil
}

// NormalizeUUIDs trims whitespace, drops empty entries, and de-duplicates
// values, WITHOUT validating UUID format. Use only where format validation
// already happens elsewhere (e.g. a DB foreign-key constraint) and the
// caller just needs a clean, de-duplicated list to work with.
func NormalizeUUIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// NormalizeAndValidateUUIDs trims whitespace, drops empty entries,
// de-duplicates, and validates every remaining entry as a UUID. Returns an
// error naming the first invalid value found; the returned slice is nil in
// that case.
func NormalizeAndValidateUUIDs(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, err := ParseUUID(value); err != nil {
			return nil, fmt.Errorf("invalid uuid: %s", value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}
