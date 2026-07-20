package adapters

import (
	"context"
	"strings"
	"testing"
)

// grepGit builds a Git adapter whose runner returns canned `git grep` output, so
// the grep operation's parsing/degradation is tested without a real repository.
func grepGit(stdout, stderr string, exit int) *Git {
	return &Git{tool: fakeTool(stdout, stderr, exit)}
}

func runGrep(t *testing.T, g *Git, pattern string, limit int) Result {
	t.Helper()
	res, err := g.Execute(context.Background(), Request{Operation: "grep",
		Input: map[string]any{"pattern": pattern, "limit": limit}})
	if err != nil {
		t.Fatalf("grep execute: %v", err)
	}
	return res
}

func TestGitGrepParsesOneCandidatePerFile(t *testing.T) {
	out := "internal/kernel/verify.go:120:func Verify() {\n" +
		"internal/kernel/verify.go:200:another match\n" +
		"internal/adapters/git.go:50:grep here\n"
	res := runGrep(t, grepGit(out, "", 0), "verify", 8)

	if res.Status != StatusAuthoritative {
		t.Fatalf("status = %s, want authoritative (%s)", res.Status, res.Summary)
	}
	// verify.go appears twice but yields a single file candidate.
	if len(res.Facts) != 2 {
		t.Fatalf("expected 2 file candidates, got %d: %s", len(res.Facts), factClaims(res))
	}
	f := res.Facts[0]
	if f.Kind != "code_location" || f.Confidence != "low" {
		t.Fatalf("fact kind/confidence = %s/%s, want code_location/low", f.Kind, f.Confidence)
	}
	if f.Location == nil || f.Location.File != "internal/kernel/verify.go" || f.Location.StartLine != 120 {
		t.Fatalf("location = %+v, want internal/kernel/verify.go:120", f.Location)
	}
	if !strings.Contains(f.Claim, `"verify"`) {
		t.Fatalf("claim should name the pattern: %s", f.Claim)
	}
}

func TestGitGrepNoMatchesIsAuthoritativeEmpty(t *testing.T) {
	// git grep exits 1 on a clean search with no matches — authoritative, not error.
	res := runGrep(t, grepGit("", "", 1), "zzz", 8)
	if res.Status != StatusAuthoritative || len(res.Facts) != 0 {
		t.Fatalf("no-match should be authoritative-empty, got status=%s facts=%d", res.Status, len(res.Facts))
	}
}

func TestGitGrepNotARepoIsPartial(t *testing.T) {
	res := runGrep(t, grepGit("", "fatal: not a git repository (or any of the parent directories): .git", 128), "x", 8)
	if res.Status != StatusPartial {
		t.Fatalf("non-repo should be partial, got %s (%s)", res.Status, res.Summary)
	}
}

func TestGitGrepUnknownErrorIsUnavailable(t *testing.T) {
	res := runGrep(t, grepGit("", "fatal: some other failure", 129), "x", 8)
	if res.Status != StatusUnavailable {
		t.Fatalf("unknown git error should be unavailable, got %s", res.Status)
	}
}

func TestGitGrepEmptyPatternErrors(t *testing.T) {
	res := runGrep(t, grepGit("", "", 0), "   ", 8)
	if res.Status != StatusError {
		t.Fatalf("empty pattern should error, got %s", res.Status)
	}
}

func TestGitGrepBoundsToLimit(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "file"+string(rune('a'+i))+".go:1:match")
	}
	res := runGrep(t, grepGit(strings.Join(lines, "\n"), "", 0), "match", 5)
	if len(res.Facts) != 5 {
		t.Fatalf("expected limit=5 candidates, got %d", len(res.Facts))
	}
}

func TestGitGrepRedactsSecretShapedSnippets(t *testing.T) {
	secret := "ghp_" + strings.Repeat("a", 30)
	res := runGrep(t, grepGit("auth.go:10: const token = \""+secret+"\"\n", "", 0), "token", 8)
	if len(res.Facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(res.Facts))
	}
	if strings.Contains(res.Facts[0].Claim, secret) {
		t.Fatalf("secret leaked into the recorded claim: %s", res.Facts[0].Claim)
	}
}

func TestParseGitGrepSkipsMalformedLines(t *testing.T) {
	out := "not-a-match-line\nfoo.go:12:real match\n:5:orphan\nbar.go:notanum:text\n"
	facts := parseGitGrep(out, "match", 8)
	// foo.go parses cleanly; "bar.go:notanum:text" still yields a file candidate
	// with line 0 (Atoi fails soft); the two malformed lines are skipped.
	files := map[string]bool{}
	for _, f := range facts {
		files[f.Location.File] = true
	}
	if !files["foo.go"] {
		t.Fatalf("foo.go should be a candidate, got %+v", facts)
	}
	if files["not-a-match-line"] || files[""] {
		t.Fatalf("malformed lines should be skipped, got %+v", facts)
	}
}
