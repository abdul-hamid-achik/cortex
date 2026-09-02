package adapters

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realRepo creates a committed git repository with a nested module for the
// real-git adapter operations (tree, head, changed-since, tracked files).
func realRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t.co")
	run("config", "user.name", "t")
	for _, f := range []string{"internal/kernel/k.go", "internal/kernel/k_test.go", "internal/cli/c.go", "README.md"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), []byte("package x // "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-qm", "init")
	return dir
}

func TestGitTreeListsModuleFilesAsFacts(t *testing.T) {
	dir := realRepo(t)
	g := NewGit()
	res, err := g.Execute(context.Background(), Request{Operation: "tree", Input: map[string]any{"dir": dir, "scope": "internal/kernel", "limit": 5}})
	if err != nil || res.Status != StatusAuthoritative {
		t.Fatalf("tree = %+v %v", res, err)
	}
	if len(res.Facts) != 3 || !strings.Contains(res.Facts[0].Claim, "module internal/kernel has 2 tracked file(s)") || !strings.Contains(res.Facts[0].Claim, "1 test file") {
		t.Fatalf("tree facts = %s", factClaims(res))
	}
	if res.Facts[1].Location == nil || res.Facts[1].Location.File != "internal/kernel/k.go" {
		t.Errorf("non-test sources should be listed first: %+v", res.Facts[1].Location)
	}
	root, _ := g.Execute(context.Background(), Request{Operation: "tree", Input: map[string]any{"dir": dir, "scope": ".", "limit": 1}})
	if root.Status != StatusAuthoritative || !strings.Contains(root.Facts[0].Claim, "repository root has 4 tracked file(s)") || len(root.Facts) != 2 {
		t.Fatalf("root tree = %s", factClaims(root))
	}
	empty, _ := g.Execute(context.Background(), Request{Operation: "tree", Input: map[string]any{"dir": dir, "scope": "nope"}})
	if empty.Status != StatusAuthoritative || len(empty.Facts) != 1 || !strings.Contains(empty.Facts[0].Claim, "no tracked files") {
		t.Fatalf("empty tree = %+v", empty)
	}
	if got := grepPathspec(" /internal/kernel/ "); len(got) != 1 || got[0] != "internal/kernel" {
		t.Errorf("grepPathspec = %v", got)
	}
	if got := grepPathspec("."); got != nil {
		t.Errorf("root pathspec should be empty: %v", got)
	}
}

func TestGitGrepHonorsScope(t *testing.T) {
	dir := realRepo(t)
	g := NewGit()
	scoped, err := g.Execute(context.Background(), Request{Operation: "grep", Input: map[string]any{"dir": dir, "pattern": "package", "limit": 8, "scope": "internal/cli"}})
	if err != nil || scoped.Status != StatusAuthoritative || len(scoped.Facts) != 1 || scoped.Facts[0].Location.File != "internal/cli/c.go" {
		t.Fatalf("scoped grep = %s (%v)", factClaims(scoped), err)
	}
	all, _ := g.Execute(context.Background(), Request{Operation: "grep", Input: map[string]any{"dir": dir, "pattern": "package", "limit": 8}})
	if len(all.Facts) != 4 {
		t.Fatalf("unscoped grep should hit every file: %s", factClaims(all))
	}
}

func TestGitHeadChangedSinceAndTrackedFiles(t *testing.T) {
	dir := realRepo(t)
	g := NewGit()
	ctx := context.Background()
	head, err := g.Head(ctx, dir)
	if err != nil || len(head) < 7 {
		t.Fatalf("head = %q %v", head, err)
	}
	files, err := g.TrackedFiles(ctx, dir)
	if err != nil || len(files) != 4 {
		t.Fatalf("tracked = %v %v", files, err)
	}
	changed, err := g.ChangedSince(ctx, dir, head)
	if err != nil || len(changed) != 0 {
		t.Fatalf("nothing changed yet: %v %v", changed, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "cli", "c.go"), []byte("package x // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err = g.ChangedSince(ctx, dir, head)
	if err != nil || strings.Join(changed, ",") != "internal/cli/c.go,new.txt" {
		t.Fatalf("dirty + untracked should be reported: %v %v", changed, err)
	}
	cmd := exec.Command("git", "commit", "-qam", "edit")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	changed, err = g.ChangedSince(ctx, dir, head)
	if err != nil || len(changed) != 2 {
		t.Fatalf("committed change must still be reported against the old commit: %v %v", changed, err)
	}
	if _, err := g.ChangedSince(ctx, dir, "0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("an unknown commit must be an error, never a falsely fresh answer")
	}
	if _, err := g.ChangedSince(ctx, dir, ""); err == nil {
		t.Fatal("empty commit must be an error")
	}
}
