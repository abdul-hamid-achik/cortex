// Package slug generates URL slugs for blog post titles.
package slug

import "strings"

// Slugify converts an article title into a URL slug: lowercase letters and
// digits separated by single dashes. Punctuation and whitespace collapse into
// one dash, and leading or trailing separators are dropped.
func Slugify(title string) string {
	var b strings.Builder
	pendingDash := false
	for i := 0; i < len(title); i++ {
		c := title[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteByte(c)
			continue
		}
		pendingDash = true
	}
	return b.String()
}
