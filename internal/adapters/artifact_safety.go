package adapters

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	// Artifact preview limits are enforced while walking, not after collecting,
	// so a hostile restored stash cannot grow an unbounded in-memory manifest.
	MaxArtifactPreviewWalkEntries = 512
	MaxArtifactPreviewFiles       = 100
	MaxArtifactPreviewPathBytes   = 1024
	MaxArtifactPreviewIDBytes     = 256
)

// ValidateArtifactID accepts only the portable token shape used by case raw
// IDs and fcheap stash IDs. URI syntax, separators, query strings, and traversal
// components are deliberately outside this grammar.
func ValidateArtifactID(id string) error {
	if id == "" {
		return fmt.Errorf("artifact id is empty")
	}
	if len(id) > MaxArtifactPreviewIDBytes {
		return fmt.Errorf("artifact id exceeds %d bytes", MaxArtifactPreviewIDBytes)
	}
	if !artifactIDAlphaNumeric(id[0]) {
		return fmt.Errorf("artifact id must start with a letter or digit")
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if artifactIDAlphaNumeric(c) || c == '_' || c == '-' {
			continue
		}
		return fmt.Errorf("artifact id contains unsafe characters")
	}
	return nil
}

func artifactIDAlphaNumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// ValidateArtifactPath requires a canonical, portable relative slash path.
// Callers must not silently clean an unsafe path because doing so can turn a
// traversal attempt into a different valid-looking file.
func ValidateArtifactPath(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > MaxArtifactPreviewPathBytes {
		return fmt.Errorf("artifact path exceeds %d bytes", MaxArtifactPreviewPathBytes)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("artifact path must not have surrounding whitespace")
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("artifact path must be valid utf-8")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("artifact path contains control characters")
		}
	}
	if strings.ContainsRune(value, 0) || strings.Contains(value, `\`) {
		return fmt.Errorf("artifact path contains unsafe characters")
	}
	if path.IsAbs(value) || strings.HasPrefix(value, "/") {
		return fmt.Errorf("artifact path must be relative")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return fmt.Errorf("artifact path must be a canonical relative path")
	}
	parts := strings.Split(value, "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("artifact path must be a canonical relative path")
		}
		// Reject a Windows volume prefix even when validation runs on Unix.
		if i == 0 && len(part) >= 2 && part[1] == ':' && ((part[0] >= 'a' && part[0] <= 'z') || (part[0] >= 'A' && part[0] <= 'Z')) {
			return fmt.Errorf("artifact path must not contain a volume prefix")
		}
	}
	return nil
}

// ArtifactContentIsBinary identifies content that cannot be safely emitted as
// terminal/model text. Invalid UTF-8 and NUL-bearing data require an explicit
// binary opt-in and are then returned as base64.
func ArtifactContentIsBinary(data []byte) bool {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	for _, magic := range [][]byte{
		{0x7f, 'E', 'L', 'F'}, {0x89, 'P', 'N', 'G'}, {0xff, 0xd8, 0xff},
		{'P', 'K', 0x03, 0x04}, {'G', 'I', 'F', '8'}, {'%', 'P', 'D', 'F', '-'},
		{0x1f, 0x8b}, {0xca, 0xfe, 0xba, 0xbe},
	} {
		if bytes.HasPrefix(data, magic) {
			return true
		}
	}
	for _, b := range data {
		if (b < 0x20 && b != '\n' && b != '\r' && b != '\t') || b == 0x7f {
			return true
		}
	}
	return false
}
