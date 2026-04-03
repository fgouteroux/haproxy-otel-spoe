package internal

// Build-time variables injected via -ldflags by the Makefile, GoReleaser,
// and CI. Defaults are safe fallbacks for local `go run .` usage.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)
