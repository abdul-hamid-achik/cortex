package adapters

import (
	"path/filepath"
	"strings"
)

// JunkDiscoveryPath reports whether a discovery hit path is tooling noise that
// should not enter a case ledger (.agent cases, build output, dependency trees).
func JunkDiscoveryPath(path string) bool {
	p := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if p == "" {
		return false
	}
	for _, bad := range []string{
		"/.agent/", "/node_modules/", "/dist/", "/.next/", "/coverage/",
		"/.cortex/", "/.vecgrep/", "/vendor/", "/__pycache__/",
		"/cases/", "commands.jsonl", "/_archive/",
	} {
		if strings.Contains(p, bad) {
			return true
		}
	}
	for _, bad := range []string{
		".agent/", "node_modules/", "dist/", ".cortex/cases",
	} {
		if strings.HasPrefix(p, bad) || strings.Contains(p, " "+bad) || strings.Contains(p, " in "+bad) {
			return true
		}
	}
	return false
}

// JunkDiscoveryFact reports whether a fact points at junk-path noise.
func JunkDiscoveryFact(f Fact) bool {
	path := ""
	if f.Location != nil {
		path = f.Location.File
	}
	if path == "" {
		path = f.URI
	}
	return JunkDiscoveryPath(path) || JunkDiscoveryPath(f.Claim)
}
