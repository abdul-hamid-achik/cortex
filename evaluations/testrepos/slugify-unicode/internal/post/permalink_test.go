package post

import "testing"

func TestPermalink(t *testing.T) {
	cases := map[string]string{
		"Hello World":            "/posts/hello-world",
		"  Go 1.25 -- Released ": "/posts/go-1-25-released",
		"Already-a-slug":         "/posts/already-a-slug",
		"":                       "/posts/",
	}
	for in, want := range cases {
		if got := Permalink(in); got != want {
			t.Errorf("Permalink(%q) = %q, want %q", in, got, want)
		}
	}
}
