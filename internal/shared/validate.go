package shared

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	ErrEmptyHostID       = errors.New("host ID must not be empty")
	ErrDescriptionTooLong = errors.New("description exceeds maximum byte length")
	ErrInvalidUTF8        = errors.New("description contains invalid UTF-8")
)

// ValidateHostID checks that a host ID is non-empty and contains no
// problematic characters (whitespace, colons used in stable IDs).
func ValidateHostID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrEmptyHostID
	}
	// Host IDs appear in stable IDs separated by colons, so colons
	// in the host ID itself would create ambiguity.
	if strings.Contains(id, ":") {
		return errors.New("host ID must not contain colons")
	}
	return nil
}

// ValidateDescription checks that a description string is valid UTF-8
// and does not exceed the maximum byte length.
func ValidateDescription(s string) error {
	if !utf8.ValidString(s) {
		return ErrInvalidUTF8
	}
	if len(s) > MaxDescriptionBytes {
		return ErrDescriptionTooLong
	}
	return nil
}
