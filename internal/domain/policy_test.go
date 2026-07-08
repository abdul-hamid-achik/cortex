package domain

import "testing"

func TestRouteFor(t *testing.T) {
	cases := []struct {
		name      string
		question  string
		surfaces  []Surface
		wantFirst string
	}{
		{"vague behavior → discover first", "the login sometimes fails silently", nil, "vecgrep"},
		{"known symbol → structure first", "ResolveReturnURL", nil, "codemap"},
		{"dotted symbol → structure first", "auth.HandleCallback", nil, "codemap"},
		{"impact question → codemap", "what breaks if I change the auth middleware", nil, "codemap"},
		{"browser surface → cairntrace", "the page redirects to the wrong place", []Surface{SurfaceBrowser}, "cairntrace"},
		{"terminal surface → glyphrun", "the CLI leaves the TUI in a bad state", []Surface{SurfaceTerminal}, "glyphrun"},
		{"bug video → vidtrace", "investigate the old bug video recording", nil, "vidtrace"},
		{"artifact question → fcheap", "recover the old run log bundle from the stash", nil, "fcheap"},
		{"secret question → tvault", "does the deploy have the api key credential", nil, "tvault"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := RouteFor(tc.question, tc.surfaces)
			if r.First != tc.wantFirst {
				t.Errorf("RouteFor(%q) first = %q, want %q (why: %s)", tc.question, r.First, tc.wantFirst, r.Why)
			}
			if r.Why == "" {
				t.Error("route should carry a rationale")
			}
		})
	}
}

func TestSurfaceVerifier(t *testing.T) {
	cases := map[Surface]string{
		SurfaceBrowser:  "cairntrace",
		SurfaceTerminal: "glyphrun",
		SurfaceArtifact: "fcheap",
		SurfaceSecret:   "tvault",
		SurfaceCode:     "codemap",
	}
	for s, want := range cases {
		if got := SurfaceVerifier(s); got != want {
			t.Errorf("SurfaceVerifier(%s) = %q, want %q", s, got, want)
		}
	}
}

func TestDefaultBudget(t *testing.T) {
	b := DefaultBudget()
	if b.MaxParallelCalls != 3 || b.MaxInvestigationRounds != 3 {
		t.Errorf("default budget mismatch: %+v", b)
	}
	if b.MaxRawOutputBytesPerTool != 32768 {
		t.Errorf("raw byte budget = %d, want 32768", b.MaxRawOutputBytesPerTool)
	}
}
