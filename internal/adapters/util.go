package adapters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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
