// Package post builds the public address of a blog post.
package post

import "strings"

// Prefix is the path every post lives under.
const Prefix = "/posts/"

// Permalink returns the public path for a post title, for example
// "/posts/hello-world". Runs of punctuation and whitespace collapse into one
// dash, and the title's letters are lowercased.
func Permalink(title string) string {
	return Prefix + fold(title)
}

func fold(title string) string {
	var b strings.Builder
	sep := false
	for i := 0; i < len(title); i++ {
		c := title[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			if sep && b.Len() > 0 {
				b.WriteByte('-')
			}
			sep = false
			b.WriteByte(c)
			continue
		}
		sep = true
	}
	return b.String()
}
