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
		{"artifact surface override → fcheap", "perform the operation", []Surface{SurfaceArtifact}, "fcheap"},
		{"secret surface override → tvault", "perform the operation", []Surface{SurfaceSecret}, "tvault"},
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
func TestRoutingMatrixDrivesRouteForInOrder(t *testing.T) {
	if len(RoutingMatrix) != 8 {
		t.Fatalf("routing matrix has %d rows, want 8", len(RoutingMatrix))
	}
	if !RoutingMatrix[len(RoutingMatrix)-1].Default {
		t.Fatal("routing matrix must end with the default route")
	}
	cases := []struct {
		rule     int
		question string
		surfaces []Surface
	}{
		{0, "page output is wrong", []Surface{SurfaceBrowser, SurfaceTerminal}},
		{1, "behavior is wrong", []Surface{SurfaceBrowser}},
		{2, "what breaks if I change auth", nil},
		{3, "auth.HandleCallback", nil},
		{4, "inspect this bug video", nil},
		{5, "recover the log bundle", nil},
		{6, "check the api key", nil},
		{7, "sporadic behavior with no clear owner", nil},
	}
	for _, tc := range cases {
		want := RoutingMatrix[tc.rule]
		got := RouteFor(tc.question, tc.surfaces)
		if got.First != want.First || got.FollowUp != want.FollowUp || got.Why != want.Why {
			t.Errorf("rule %d mismatch: got %+v, want %s → %s (%s)", tc.rule, got, want.First, want.FollowUp, want.Why)
		}
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
