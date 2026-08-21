package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// pluralize renders "N thing" / "N things" with the count.
func pluralize(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// decodeJSON unmarshals tool stdout into v, returning a wrapped error that keeps
// the operation legible when a tool changes its output shape.
func decodeJSON(stdout string, v any) error {
	if err := json.Unmarshal([]byte(stdout), v); err != nil {
		return fmt.Errorf("parse tool json: %w", err)
	}
	return nil
}

// firstNonEmpty returns the first non-empty string, or "".
func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// clip truncates a string to n runes with an ellipsis, for compact summaries.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// firstLine returns the first non-empty, trimmed line of s. Used by version
// probes and first-line-of-stderr degradation.
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// withFix attaches a suggested repair command onto the first fact's Attributes
// so setup/status/investigate can surface the specialist's own hint instead of
// a generic "run index".
func withFix(res Result, fix string) Result {
	fix = strings.TrimSpace(fix)
	if fix == "" {
		return res
	}
	if len(res.Facts) == 0 {
		res.Facts = []Fact{{Kind: "tool_unavailable", Confidence: "unknown", Claim: res.Summary}}
	}
	if res.Facts[0].Attributes == nil {
		res.Facts[0].Attributes = map[string]string{}
	}
	res.Facts[0].Attributes["fix"] = fix
	if res.Facts[0].Attributes["index"] == "" {
		res.Facts[0].Attributes["index"] = "needs_index"
	}
	return res
}

// failExec maps a runner error to timedOut (honest slow/hung) or unavailable.
func failExec(tool, op string, err error, budget time.Duration) Result {
	if isTimeout(err) {
		return timedOut(tool, op, budget)
	}
	return unavailable(tool, op, err.Error())
}

// isTimeout reports whether err is (or wraps) a deadline exceeded.
func isTimeout(err error) bool {
	return err != nil && errors.Is(err, context.DeadlineExceeded)
}

// TimedOut is an honest partial result: the specialist may be slow or hung,
// not necessarily unindexed. Setup must not recommend `init`/`index` for this.
func TimedOut(tool, op string, budget time.Duration) Result {
	return timedOut(tool, op, budget)
}

// timedOut is an honest partial result: the specialist may be slow or hung,
// not necessarily unindexed. Setup must not recommend `init`/`index` for this.
func timedOut(tool, op string, budget time.Duration) Result {
	msg := fmt.Sprintf("%s %s timed out after %s — specialist may be slow, not necessarily unindexed",
		tool, op, budget.Round(time.Millisecond))
	return Result{
		Tool: tool, Operation: op, Status: StatusPartial, Summary: msg,
		Warnings: []string{msg},
		Facts: []Fact{{
			Kind: "tool_unavailable", Confidence: "unknown", Claim: msg,
			Attributes: map[string]string{"index": "timeout"},
		}},
	}
}

// IsTimeoutResult reports whether a Result was classified as an adapter timeout.
func IsTimeoutResult(res Result) bool {
	if strings.Contains(strings.ToLower(res.Summary), "timed out") {
		return true
	}
	for _, f := range res.Facts {
		if f.Attributes["index"] == "timeout" {
			return true
		}
	}
	return false
}

// fixFromText pulls a backticked command from specialist error text, falling
// back to a safe default. Prefer the tool's own hint when present.
func fixFromText(text, fallback string) string {
	lower := strings.ToLower(text)
	// Prefer the longest backtick command that looks like a tool invocation.
	best := ""
	for {
		start := strings.Index(text, "`")
		if start < 0 {
			break
		}
		rest := text[start+1:]
		end := strings.Index(rest, "`")
		if end < 0 {
			break
		}
		cmd := strings.TrimSpace(rest[:end])
		text = rest[end+1:]
		if cmd == "" {
			continue
		}
		if strings.Contains(cmd, "codemap") || strings.Contains(cmd, "vecgrep") {
			if len(cmd) > len(best) {
				best = cmd
			}
		}
	}
	if best != "" {
		return best
	}
	switch {
	case strings.Contains(lower, "reset --force"):
		return "vecgrep reset --force && vecgrep index"
	case strings.Contains(lower, "index --reindex"):
		return "codemap index --reindex"
	case strings.Contains(lower, "vecgrep init"):
		return "vecgrep init && vecgrep index"
	case strings.Contains(lower, "codemap index"):
		return "codemap index"
	}
	return fallback
}

// containsFold is a case-insensitive substring test.
func containsFold(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if strings.EqualFold(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

// requireFields verifies that each named key is present and non-null in a JSON
// object, returning an error naming any that are missing. Adapters call it after
// decodeJSON to catch schema drift: if a tool renames a field the adapter relies
// on (e.g. codemap's "found"), a plain unmarshal silently reads a zero value and
// could report a confidently-wrong "not found"; this check makes the adapter
// degrade loudly instead. It complements decodeJSON, which only catches output
// that is not valid JSON at all.
func requireFields(stdout string, required ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &fields); err != nil {
		return err
	}
	var missing []string
	for _, key := range required {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required field(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// schemaDrift builds a partial result for parseable JSON that is missing a field
// the adapter depends on (a tool schema rename). Degrading loudly here prevents
// a silently-zeroed field from becoming a confidently-wrong conclusion. The raw
// (already redacted) output is kept as evidence.
func schemaDrift(tool, op string, err error, stdout string) Result {
	return Result{
		Tool: tool, Operation: op, Status: StatusPartial,
		Summary:  fmt.Sprintf("%s %s returned an unexpected output shape: %s", tool, op, err.Error()),
		Warnings: []string{tool + ": " + clip(err.Error(), 160)},
		Raw:      stdout,
	}
}
