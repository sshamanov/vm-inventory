// Package version provides build-time version information injected via ldflags.
//
//	go build -ldflags "-X vm-inventory/internal/version.Version=v0.1.0 -X vm-inventory/internal/version.Commit=abc123" ./cmd/...
package version

var (
	Version = "dev"
	Commit  = "unknown"
)
