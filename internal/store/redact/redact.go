// Package redact removes secret-shaped material from any text before it reaches
// model-visible output or a case file (SPEC §16). Cortex never stores raw
// secrets; this is the last-line filter for tool stderr/stdout that may leak
// tokens despite tvault's boundary.
package redact

import (
	"regexp"
	"strings"
)

// Mask is the replacement written in place of a detected secret.
const Mask = "«redacted»"

// pattern pairs a compiled regexp with a human label for auditing.
type pattern struct {
	name string
	re   *regexp.Regexp
}

// builtins are conservative, high-signal secret shapes. They favor precision
// over recall — a false negative is a leak, but an over-eager pattern that
// masks ordinary code is its own failure, so these target well-known formats.
var builtins = []pattern{
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\bgh[posru]_[0-9A-Za-z]{20,255}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}\b`)},
	{"stripe-key", regexp.MustCompile(`\b(?:sk|rk|pk)_(?:live|test)_[0-9A-Za-z]{16,}\b`)},
	{"openai-key", regexp.MustCompile(`\bsk-(?:proj-)?[0-9A-Za-z_-]{20,}\b`)},
	{"private-key-block", regexp.MustCompile(`(?s)-----BEGIN[^-]+PRIVATE KEY-----.*?-----END[^-]+PRIVATE KEY-----`)},
	{"jwt", regexp.MustCompile(`\beyJ[0-9A-Za-z_-]{10,}\.[0-9A-Za-z_-]{10,}\.[0-9A-Za-z_-]{10,}\b`)},
	{"bearer", regexp.MustCompile(`(?i)\bbearer\s+[0-9A-Za-z._~+/-]{16,}=*`)},
	// KEY=secret / KEY: secret assignments where the name signals a secret. The
	// value matches a double-quoted, single-quoted, OR bare token — each consumed
	// whole so an embedded quote can't truncate the mask and leak the tail, and a
	// single-quoted value isn't skipped (RE2 has no backreferences, hence the
	// explicit three-way alternation). The optional ["'] after the key name lets
	// a JSON field ("api_key":"…") match — otherwise the key's closing quote sits
	// between the name and the ':' and defeats the separator, leaking the value.
	{"assigned-secret", regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:SECRET|TOKEN|PASSWORD|PASSWD|API[_-]?KEY|PRIVATE[_-]?KEY|ACCESS[_-]?KEY)[A-Z0-9_]*)["']?\s*([:=])\s*(?:"[^"\n]{3,}"|'[^'\n]{3,}'|[^\s]{6,})`)},
}

// Redactor masks secrets. Extra literal values (e.g. secret names/values known
// to tvault at inject time) are masked in addition to the built-in patterns.
type Redactor struct {
	patterns []pattern
	literals []string
}

// New returns a redactor seeded with the built-in patterns plus any exact
// literal strings that must always be masked (case-sensitive, longest first).
func New(literals ...string) *Redactor {
	r := &Redactor{patterns: builtins}
	for _, l := range literals {
		if strings.TrimSpace(l) != "" {
			r.literals = append(r.literals, l)
		}
	}
	// Longest literals first so a substring doesn't pre-empt its superstring.
	sortByLenDesc(r.literals)
	return r
}

// String masks all detected secrets in s. The assigned-secret pattern keeps the
// key name so the redacted line stays readable (KEY=«redacted») while masking
// the entire value regardless of quoting.
func (r *Redactor) String(s string) string {
	for _, l := range r.literals {
		s = strings.ReplaceAll(s, l, Mask)
	}
	for _, p := range r.patterns {
		if p.name == "assigned-secret" {
			// Keep the key name and the original separator so the redacted line
			// stays readable and structurally intact — `KEY=«redacted»` for an
			// env assignment, `"api_key":«redacted»` for a JSON field (the closing
			// key quote is consumed by the optional ["'] in the pattern). The mask
			// replaces the whole value regardless of quoting so nothing survives.
			s = p.re.ReplaceAllString(s, `${1}${2}`+Mask)
			continue
		}
		s = p.re.ReplaceAllString(s, Mask)
	}
	return s
}

// Detected reports whether s contains anything the redactor would mask. Useful
// for tagging evidence sensitivity without exposing the match.
func (r *Redactor) Detected(s string) bool {
	return r.String(s) != s
}

func sortByLenDesc(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && len(xs[j]) > len(xs[j-1]); j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
