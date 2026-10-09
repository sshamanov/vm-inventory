package shared

import (
	"testing"
	"time"
)

func TestParseUIWindow(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		want   time.Duration
		wantOK bool
	}{
		// The default view has to keep the meaning a request without the
		// parameter had before the switch existed.
		{"absent means now", "", UIWindowNow, true},
		{"now", "now", UIWindowNow, true},
		{"month", "month", UIWindowMonth, true},
		{"all", "all", UIWindowAll, true},
		{"unknown is rejected", "week", 0, false},
		{"empty-ish is rejected", " ", 0, false},
		{"case is not folded", "Now", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseUIWindow(tt.value)
			if ok != tt.wantOK {
				t.Fatalf("ParseUIWindow(%q) ok = %v, want %v", tt.value, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("ParseUIWindow(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// TestUIWindowNamesRoundTrip keeps the switch's list and the parser in step: a
// name the switch offers but the parser rejects would be a 400 in the browser.
func TestUIWindowNamesRoundTrip(t *testing.T) {
	for _, name := range UIWindowNames {
		if _, ok := ParseUIWindow(name); !ok {
			t.Errorf("UIWindowNames offers %q but ParseUIWindow rejects it", name)
		}
	}
	if UIWindowNames[0] != "now" {
		t.Errorf("UIWindowNames[0] = %q, want the default view first", UIWindowNames[0])
	}
}

// TestRetentionOutlastsEveryView guards the prune window: pruning at the widest
// view would delete an instance on the day the "all" view could still ask for
// it (§15.4).
func TestRetentionOutlastsEveryView(t *testing.T) {
	for _, name := range UIWindowNames {
		window, _ := ParseUIWindow(name)
		if RetentionWindow <= window {
			t.Errorf("RetentionWindow %v does not outlast the %q view %v", RetentionWindow, name, window)
		}
	}
}
