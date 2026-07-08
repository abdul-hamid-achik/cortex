package adapters

import (
	"encoding/json"
	"fmt"
	"strconv"
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
