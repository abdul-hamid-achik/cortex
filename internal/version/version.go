// Package version holds build metadata injected via -ldflags at build time.
package version

import "fmt"

// These are set by the linker (see Taskfile.yml LDFLAGS). Defaults are for
// `go run` / `go test` builds that don't pass ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Full returns a human-readable version string for --version output.
func Full() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
