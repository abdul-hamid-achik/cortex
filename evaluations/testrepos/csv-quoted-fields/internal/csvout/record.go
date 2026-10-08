package csvout

import "strings"

// Record renders values as one comma-separated record, without a line
// terminator.
func Record(values []string) string {
	fields := make([]string, len(values))
	for i, v := range values {
		fields[i] = Field(v)
	}
	return strings.Join(fields, ",")
}
