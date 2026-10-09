package shared

import "testing"

func TestValidateHostID(t *testing.T) {
	tests := []struct {
		id      string
		wantErr bool
	}{
		{"hv-kvm-01", false},
		{"esxi-01", false},
		{"host_123", false},
		{"host.with.dots", false},
		{"", true},
		{"   ", true},
		{"host:with:colons", true},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			err := ValidateHostID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateHostID(%q) error = %v, wantErr = %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestValidateDescription(t *testing.T) {
	tests := []struct {
		desc    string
		wantErr bool
	}{
		{"", false},
		{"Main KVM host", false},
		{"hôte café", false},
		{string(make([]byte, MaxDescriptionBytes)), false},
		{string(make([]byte, MaxDescriptionBytes+1)), true},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			err := ValidateDescription(tt.desc)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateDescription(len=%d) error = %v, wantErr = %v",
					len(tt.desc), err, tt.wantErr)
			}
		})
	}
}
