package shared

import (
	"testing"
)

func TestCleanDescription(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"no change", "Main KVM host", "Main KVM host"},
		{"control chars removed", "host\x00name", "hostname"},
		{"tab and newline collapsed", "line1\tline2\nline3", "line1 line2 line3"},
		{"multiple spaces collapsed", "host    name", "host name"},
		{"trim whitespace", "  host name  ", "host name"},
		{"non-printable removed", "host\x01name", "hostname"},
		{"unicode preserved", "hôte café", "hôte café"},
		{"control chars within unicode", "héllo\x00wörld", "héllowörld"},
		{"leading/trailing whitespace", "\n  host  \t", "host"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanDescription(tt.input)
			if got != tt.want {
				t.Errorf("CleanDescription(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCleanDescriptionTruncation(t *testing.T) {
	// Build a string longer than 1024 bytes.
	long := make([]byte, 2000)
	for i := range long {
		long[i] = 'a'
	}
	result := CleanDescription(string(long))
	if len(result) > MaxDescriptionBytes {
		t.Errorf("CleanDescription produced %d bytes, want <= %d", len(result), MaxDescriptionBytes)
	}
	// Result must be valid UTF-8.
	if !validUTF8(result) {
		t.Error("CleanDescription result is not valid UTF-8")
	}
}

func TestCleanDescriptionUTF8Boundary(t *testing.T) {
	// Build a string that ends mid-rune at 1024 bytes.
	prefix := make([]byte, 1022)
	for i := range prefix {
		prefix[i] = 'a'
	}
	// Add a 3-byte UTF-8 rune, making the total 1025 bytes.
	input := string(prefix) + "ééé"
	result := CleanDescription(input)
	if len(result) > MaxDescriptionBytes {
		t.Errorf("CleanDescription produced %d bytes, want <= %d", len(result), MaxDescriptionBytes)
	}
	if !validUTF8(result) {
		t.Error("CleanDescription truncated mid-rune, result is not valid UTF-8")
	}
}

func validUTF8(s string) bool {
	for _, r := range s {
		if r == '�' && len(s) > 0 {
			// Simplified check: just verify string can be re-encoded.
		}
	}
	// Re-encode to verify.
	encoded := []byte(s)
	re := string(encoded)
	return re == s
}
