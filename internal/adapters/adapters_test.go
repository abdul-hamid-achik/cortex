package adapters

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// slowHealthAdapter records peak concurrency of Health() calls.
type slowHealthAdapter struct {
	name string
	cur  *int32
	peak *int32
}

func (a slowHealthAdapter) Name() string               { return a.name }
func (a slowHealthAdapter) Capabilities() []Capability { return nil }
func (a slowHealthAdapter) Execute(context.Context, Request) (Result, error) {
	return Result{}, nil
}
func (a slowHealthAdapter) Health(context.Context) error {
	n := atomic.AddInt32(a.cur, 1)
	for {
		p := atomic.LoadInt32(a.peak)
		if n <= p || atomic.CompareAndSwapInt32(a.peak, p, n) {
			break
		}
	}
	time.Sleep(15 * time.Millisecond)
	atomic.AddInt32(a.cur, -1)
	return nil
}

func TestRegistryHealthBounded(t *testing.T) {
	// SPEC §7.3 max_parallel_calls: the health fan-out must not exceed the bound.
	var cur, peak int32
	var as []Adapter
	for i := 0; i < 8; i++ {
		as = append(as, slowHealthAdapter{name: fmt.Sprintf("t%d", i), cur: &cur, peak: &peak})
	}
	r := NewRegistry(as...)
	r.SetMaxParallel(2)
	r.Health(context.Background())
	if p := atomic.LoadInt32(&peak); p > 2 {
		t.Errorf("health fan-out exceeded max_parallel=2: peak=%d", p)
	}
}

// fakeRunner returns canned process output for adapter contract tests.
type fakeRunner struct {
	stdout, stderr string
	exit           int
	err            error
}

func (f fakeRunner) run(context.Context, string, string, ...string) ([]byte, []byte, int, error) {
	return []byte(f.stdout), []byte(f.stderr), f.exit, f.err
}

func TestRequestAccessors(t *testing.T) {
	r := Request{Input: map[string]any{
		"s": "hello", "n": 7, "f": float64(3), "xs": []any{"a", "b"},
	}}
	if r.Str("s") != "hello" {
		t.Error("Str failed")
	}
	if r.Int("n", 0) != 7 || r.Int("f", 0) != 3 || r.Int("missing", 42) != 42 {
		t.Error("Int failed")
	}
	if got := r.StrSlice("xs"); len(got) != 2 || got[0] != "a" {
		t.Errorf("StrSlice failed: %v", got)
	}
}

func TestUnavailableResult(t *testing.T) {
	r := unavailable("cairntrace", "run", "not on PATH")
	if r.Status != StatusUnavailable {
		t.Errorf("status = %s", r.Status)
	}
	if len(r.Facts) != 1 || r.Facts[0].Kind != "tool_unavailable" {
		t.Error("unavailable should record a tool_unavailable fact, not fabricate output")
	}
}

func TestDegradedFirstLineOnly(t *testing.T) {
	r := degraded("vecgrep", "search", "", "Error: not initialized\nUsage:\n  vecgrep search\n  ...many lines...", 1)
	if r.Status != StatusPartial {
		t.Errorf("status = %s", r.Status)
	}
	if len(r.Warnings) != 1 || strings.Contains(r.Warnings[0], "Usage") {
		t.Errorf("degraded warning should be first line only, got %q", r.Warnings[0])
	}
}

func TestExecRedactsSecrets(t *testing.T) {
	// git is on PATH in dev/CI; the fake runner intercepts so it isn't invoked.
	// The token is assembled at runtime so the source holds no secret-shaped
	// literal (GitHub push protection flags test fixtures that look like real keys).
	ghp := "ghp_" + "16C7e42F292c6912E7710c838347Ae178B4a"
	tl := tool{bin: "git", run: fakeRunner{stdout: "token " + ghp + " done"}, redact: redact.New(), timeout: time.Second}
	out, _, _, err := tl.exec(context.Background(), "", "whatever")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ghp) {
		t.Errorf("secret leaked through exec: %q", out)
	}
	if !strings.Contains(out, redact.Mask) {
		t.Errorf("expected redaction mask in %q", out)
	}
}

// countingRunner fails for the first `failFirst` calls, then succeeds.
type countingRunner struct {
	calls     int
	failFirst int
	stdout    string
}

func (c *countingRunner) run(context.Context, string, string, ...string) ([]byte, []byte, int, error) {
	c.calls++
	if c.calls <= c.failFirst {
		return nil, nil, -1, context.DeadlineExceeded
	}
	return []byte(c.stdout), nil, 0, nil
}

func TestExecRetriesReadOnlyOnce(t *testing.T) {
	// SPEC §17.3: a read-only query retries once on a transient failure.
	r := &countingRunner{failFirst: 1, stdout: "ok"}
	tl := tool{bin: "git", run: r, redact: redact.New()}
	out, _, _, err := tl.exec(context.Background(), "", "x")
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if out != "ok" || r.calls != 2 {
		t.Errorf("expected exactly 2 attempts and success, got calls=%d out=%q", r.calls, out)
	}
}

func TestExecOnceDoesNotRetry(t *testing.T) {
	// Writes use execOnce and must NOT retry (no double write).
	r := &countingRunner{failFirst: 1, stdout: "ok"}
	tl := tool{bin: "git", run: r, redact: redact.New()}
	_, _, _, err := tl.execOnce(context.Background(), "", "x")
	if err == nil {
		t.Error("execOnce should not retry a transient failure")
	}
	if r.calls != 1 {
		t.Errorf("execOnce should run exactly once, got %d", r.calls)
	}
}

func TestExecReturnsFullOutputForParsing(t *testing.T) {
	// The SPEC §7.3 raw cap bounds only the raw *stored* for the case file — it
	// must NOT truncate the string the adapter parses, or valid-but-large JSON
	// would be corrupted into an unparseable blob. exec returns the full output
	// (bounded only by the 4 MiB memory backstop, applied by the real runner).
	big := `{"stashes":[` + strings.Repeat(`{"id":"x"},`, 5000) + `{"id":"last"}]}`
	tl := tool{bin: "git", run: fakeRunner{stdout: big}, redact: redact.New()}
	out, _, _, err := tl.exec(context.Background(), "", "x")
	if err != nil {
		t.Fatal(err)
	}
	if out != big {
		t.Errorf("exec must not truncate the parse output: got %d bytes, want %d", len(out), len(big))
	}
	if strings.Contains(out, "truncated") {
		t.Error("exec must not inject a truncation marker into the parse output")
	}
}

func TestToolVersion(t *testing.T) {
	tl := tool{bin: "git", run: fakeRunner{stdout: "codemap version 1.2.3\nbuilt X"}, redact: redact.New()}
	if v := tl.Version(context.Background()); v != "codemap version 1.2.3" {
		t.Errorf("Version should return the first line, got %q", v)
	}
	// A failing probe yields "" (best-effort, never errors).
	bad := tool{bin: "git", run: fakeRunner{err: context.DeadlineExceeded}, redact: redact.New()}
	if v := bad.Version(context.Background()); v != "" {
		t.Errorf("a failed version probe should return empty, got %q", v)
	}
}

func TestExecMissingBinary(t *testing.T) {
	tl := tool{bin: "definitely-not-a-real-binary-xyz", run: fakeRunner{}, redact: redact.New()}
	if _, _, _, err := tl.exec(context.Background(), "", "x"); err != ErrToolMissing {
		t.Errorf("expected ErrToolMissing, got %v", err)
	}
}

func TestGitStatusAndChangedFiles(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@t.co")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "init")

	g := NewGit()
	info, err := g.Status(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsRepo || info.Commit == "" {
		t.Errorf("expected a repo with a commit, got %+v", info)
	}

	// Modify a tracked file and add an untracked one.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nvar X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := g.ChangedFiles(context.Background(), dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	// Both the modified tracked file and the untracked file must appear.
	joined := strings.Join(changed, ",")
	if !strings.Contains(joined, "a.go") || !strings.Contains(joined, "b.go") {
		t.Errorf("expected a.go and b.go in changed files, got %v", changed)
	}
}

func TestGitNonRepoDegrades(t *testing.T) {
	g := NewGit()
	info, err := g.Status(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("non-repo should degrade, not error: %v", err)
	}
	if info.IsRepo {
		t.Error("empty dir should not be a repo")
	}
}

func TestGitReviewHelpers(t *testing.T) {
	// These exercise the real git binary in a temp repo (skip if git absent).
	if _, err := execLookGit(); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	sh := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	sh("init", "-q", "-b", "main")
	sh("config", "user.email", "t@t.co")
	sh("config", "user.name", "t")
	if err := os.WriteFile(dir+"/a.txt", []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh("add", "-A")
	sh("commit", "-qm", "base")
	sh("checkout", "-q", "-b", "feat")
	if err := os.WriteFile(dir+"/a.txt", []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh("add", "-A")
	sh("commit", "-qm", "change")

	g := NewGit()
	ctx := context.Background()
	if b, err := g.CurrentBranch(ctx, dir); err != nil || b != "feat" {
		t.Errorf("CurrentBranch = %q, %v", b, err)
	}
	if db := g.DefaultBranch(ctx, dir, ""); db != "main" {
		t.Errorf("DefaultBranch = %q, want main", db)
	}
	mb, err := g.MergeBase(ctx, dir, "main", "feat")
	if err != nil || len(mb) < 7 {
		t.Errorf("MergeBase = %q, %v", mb, err)
	}
	// base…HEAD diff sees the one changed file.
	files, err := g.ChangedFiles(ctx, dir, mb, false)
	if err != nil || len(files) != 1 || files[0] != "a.txt" {
		t.Errorf("ChangedFiles(since=merge-base) = %v, %v", files, err)
	}
}

func execLookGit() (string, error) { return exec.LookPath("git") }

func TestGitChangedFilesErrorsOnBadBase(t *testing.T) {
	if _, err := execLookGit(); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	sh := func(a ...string) { c := exec.Command("git", a...); c.Dir = dir; _ = c.Run() }
	sh("init", "-q", "-b", "main")
	sh("config", "user.email", "t@t.co")
	sh("config", "user.name", "t")
	_ = os.WriteFile(dir+"/a.txt", []byte("1\n"), 0o644)
	sh("add", "-A")
	sh("commit", "-qm", "base")
	g := NewGit()
	// A bad/unresolvable base ref must ERROR, not return an empty (false all-clear).
	if _, err := g.ChangedFiles(context.Background(), dir, "deadbeefbadref", false); err == nil {
		t.Error("ChangedFiles against a bad base ref must return an error, not 0 files")
	}
	// Untracked scratch files are NOT part of a committed base diff.
	_ = os.WriteFile(dir+"/scratch.tmp", []byte("x"), 0o644)
	base, _ := g.MergeBase(context.Background(), dir, "main", "HEAD")
	files, _ := g.ChangedFiles(context.Background(), dir, base, false)
	for _, f := range files {
		if f == "scratch.tmp" {
			t.Error("a base diff must not include untracked working-tree files")
		}
	}
}

func TestGitDefaultBranchMasterOnly(t *testing.T) {
	if _, err := execLookGit(); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	sh := func(a ...string) { c := exec.Command("git", a...); c.Dir = dir; _ = c.Run() }
	sh("init", "-q", "-b", "master") // master-only, no main
	sh("config", "user.email", "t@t.co")
	sh("config", "user.name", "t")
	_ = os.WriteFile(dir+"/a.txt", []byte("1\n"), 0o644)
	sh("add", "-A")
	sh("commit", "-qm", "base")
	if db := NewGit().DefaultBranch(context.Background(), dir, ""); db != "master" {
		t.Errorf("DefaultBranch on a master-only repo = %q, want master (the main/master fallback was dead)", db)
	}
}
