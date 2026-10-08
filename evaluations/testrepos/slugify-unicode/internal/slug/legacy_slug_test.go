package slug

import "testing"

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello World":           "hello-world",
		"  Go 1.25 -- Released": "go-1-25-released",
		"Already-a-slug":        "already-a-slug",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
