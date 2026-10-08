// Package csvout writes comma-separated records for spreadsheet import.
package csvout

import "strings"

// Field renders one value for use inside a record. Values that would be
// ambiguous next to the separator are wrapped in double quotes, and any
// double quote inside such a value is escaped.
func Field(s string) string {
	if !needsQuoting(s) {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func needsQuoting(s string) bool {
	return strings.ContainsAny(s, `,"`)
}
