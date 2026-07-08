package forge

import (
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := map[string]string{
		"git@github.com:me/repo.git":               "github",
		"https://github.com/me/repo.git":           "github",
		"git@bitbucket.org:me/repo.git":            "bitbucket",
		"https://user@bitbucket.org/me/repo.git":   "bitbucket",
		"https://bitbucket.mycorp.com/scm/p/r.git": "bitbucket",
		"git@gitlab.com:me/repo.git":               "git",
		"https://git.internal.example/me/repo.git": "git",
	}
	for url, want := range cases {
		if got := Detect(url).Name; got != want {
			t.Errorf("Detect(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestPRHeadRefspecs(t *testing.T) {
	gh := Detect("github.com/x/y").PRHeadRefspecs(42, "cortex/pr-42")
	if len(gh) != 1 || gh[0] != "refs/pull/42/head:cortex/pr-42" {
		t.Errorf("github refspec = %v", gh)
	}
	bb := Detect("bitbucket.org/x/y").PRHeadRefspecs(7, "cortex/pr-7")
	if len(bb) == 0 || !strings.HasPrefix(bb[0], "refs/pull-requests/7/from:") {
		t.Errorf("bitbucket refspec = %v", bb)
	}
}
