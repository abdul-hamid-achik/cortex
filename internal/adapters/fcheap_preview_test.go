package adapters

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

type restoreRunner struct{}

func (restoreRunner) run(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, int, error) {
	target := ""
	for i := range args {
		if args[i] == "--to" && i+1 < len(args) {
			target = args[i+1]
		}
	}
	if target == "" {
		return nil, []byte("missing target"), 2, nil
	}
	if err := os.MkdirAll(filepath.Join(target, "nested"), 0o755); err != nil {
		return nil, nil, -1, err
	}
	if err := os.WriteFile(filepath.Join(target, "nested", "final.txt"), []byte("TOKEN=supersecretvalue\nvisible\n"), 0o644); err != nil {
		return nil, nil, -1, err
	}
	return []byte(`{"ok":true}`), nil, 0, nil
}

type fixtureRestoreRunner struct {
	setup func(string) error
	calls int
}

func (r *fixtureRestoreRunner) run(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, int, error) {
	r.calls++
	target := ""
	for i := range args {
		if args[i] == "--to" && i+1 < len(args) {
			target = args[i+1]
		}
	}
	if target == "" {
		return nil, []byte("missing target"), 2, nil
	}
	if err := r.setup(target); err != nil {
		return nil, nil, -1, err
	}
	return []byte(`{"ok":true}`), nil, 0, nil
}

func TestFcheapPreviewIsBoundedAndRedacted(t *testing.T) {
	f := &Fcheap{tool: tool{bin: "git", run: restoreRunner{}, redact: redact.New(), timeout: time.Second}}
	preview, err := f.Preview(context.Background(), t.TempDir(), "fcheap://stash/stash_1", "nested/final.txt", 20)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Selected != "nested/final.txt" || preview.Encoding != "text" || !preview.Truncated {
		t.Fatalf("preview metadata = %+v", preview)
	}
	if preview.Content == "" || strings.Contains(preview.Content, "supersecret") {
		t.Fatalf("preview was not redacted: %+v", preview)
	}
}

func TestFcheapPreviewRejectsMissingSelector(t *testing.T) {
	f := &Fcheap{tool: tool{bin: "git", run: restoreRunner{}, redact: redact.New(), timeout: time.Second}}
	if _, err := f.Preview(context.Background(), t.TempDir(), "stash_1", "missing.txt", 100); err == nil {
		t.Fatal("missing artifact selector must fail")
	}
}

func TestFcheapPreviewRefusesBinaryWithoutExplicitOptIn(t *testing.T) {
	runner := &fixtureRestoreRunner{setup: func(target string) error {
		return os.WriteFile(filepath.Join(target, "image.bin"), []byte{0xff, 0x00, 0x80, 0x01}, 0o644)
	}}
	f := &Fcheap{tool: tool{bin: "git", run: runner, redact: redact.New(), timeout: time.Second}}
	if _, err := f.Preview(context.Background(), t.TempDir(), "stash_binary", "image.bin", 2); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary preview without opt-in must fail, got %v", err)
	}
	preview, err := f.PreviewWithOptions(context.Background(), t.TempDir(), "stash_binary", "image.bin", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(preview.Content)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Encoding != "base64" || !preview.Truncated || len(decoded) != 2 {
		t.Fatalf("binary opt-in preview = %+v decoded=%v", preview, decoded)
	}
}

func TestFcheapPreviewWithPathInspectsOnlyThatPath(t *testing.T) {
	runner := &fixtureRestoreRunner{setup: func(target string) error {
		if err := os.WriteFile(filepath.Join(target, "selected.txt"), []byte("selected"), 0o644); err != nil {
			return err
		}
		unrelated := filepath.Join(target, "unrelated")
		if err := os.Mkdir(unrelated, 0o755); err != nil {
			return err
		}
		for i := 0; i <= MaxArtifactPreviewWalkEntries; i++ {
			if err := os.WriteFile(filepath.Join(unrelated, fmt.Sprintf("file-%04d.txt", i)), []byte("x"), 0o644); err != nil {
				return err
			}
		}
		return nil
	}}
	f := &Fcheap{tool: tool{bin: "git", run: runner, redact: redact.New(), timeout: time.Second}}
	preview, err := f.Preview(context.Background(), t.TempDir(), "stash_selected", "selected.txt", 32)
	if err != nil {
		t.Fatalf("explicit path should not walk unrelated entries: %v", err)
	}
	if preview.Selected != "selected.txt" || len(preview.Files) != 1 || preview.Content != "selected" {
		t.Fatalf("explicit path preview = %+v", preview)
	}
}

func TestFcheapPreviewListingRejectsStrictCaps(t *testing.T) {
	tests := []struct {
		name  string
		setup func(string) error
		want  string
	}{
		{name: "regular files", want: "regular files", setup: func(target string) error {
			for i := 0; i <= MaxArtifactPreviewFiles; i++ {
				if err := os.WriteFile(filepath.Join(target, fmt.Sprintf("file-%03d.txt", i)), []byte("x"), 0o644); err != nil {
					return err
				}
			}
			return nil
		}},
		{name: "walked entries", want: "walked entries", setup: func(target string) error {
			for i := 0; i <= MaxArtifactPreviewWalkEntries; i++ {
				if err := os.Mkdir(filepath.Join(target, fmt.Sprintf("dir-%04d", i)), 0o755); err != nil {
					return err
				}
			}
			return nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fixtureRestoreRunner{setup: tt.setup}
			f := &Fcheap{tool: tool{bin: "git", run: runner, redact: redact.New(), timeout: time.Second}}
			if _, err := f.Preview(context.Background(), t.TempDir(), "stash_caps", "", 32); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("listing over %s cap should fail, got %v", tt.name, err)
			}
		})
	}
}

func TestFcheapPreviewRejectsUnsafePathsStashIDsAndSymlinks(t *testing.T) {
	runner := &fixtureRestoreRunner{setup: func(target string) error {
		if err := os.WriteFile(filepath.Join(target, "real.txt"), []byte("real"), 0o644); err != nil {
			return err
		}
		return os.Symlink("real.txt", filepath.Join(target, "link.txt"))
	}}
	f := &Fcheap{tool: tool{bin: "git", run: runner, redact: redact.New(), timeout: time.Second}}
	for _, artifactPath := range []string{"../secret", "/absolute", "./real.txt", `nested\real.txt`, "C:/secret"} {
		calls := runner.calls
		if _, err := f.Preview(context.Background(), t.TempDir(), "stash_safe", artifactPath, 32); err == nil {
			t.Errorf("unsafe path %q was accepted", artifactPath)
		}
		if runner.calls != calls {
			t.Errorf("unsafe path %q reached restore", artifactPath)
		}
	}
	for _, stash := range []string{"", "../stash", "fcheap://stash/a/b", "stash?query"} {
		calls := runner.calls
		if _, err := f.Preview(context.Background(), t.TempDir(), stash, "real.txt", 32); err == nil {
			t.Errorf("unsafe stash id %q was accepted", stash)
		}
		if runner.calls != calls {
			t.Errorf("unsafe stash id %q reached restore", stash)
		}
	}
	if _, err := f.Preview(context.Background(), t.TempDir(), "stash_safe", "link.txt", 32); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("selected symlink must fail, got %v", err)
	}
	if _, err := f.Preview(context.Background(), t.TempDir(), "stash_safe", "", 32); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("listing containing symlink must fail, got %v", err)
	}
}

func TestFcheapVerifyRejectsFlagLikeOrUnsafeStashBeforeExec(t *testing.T) {
	runner := &countingRunner{stdout: `{"id":"safe","files":[]}`}
	f := &Fcheap{tool: tool{bin: "git", run: runner, redact: redact.New(), timeout: time.Second}}
	for _, stash := range []string{"--help", "../safe", "fcheap://stash/a/b", "safe?query"} {
		result, err := f.Execute(context.Background(), Request{Operation: "verify", Input: map[string]any{"stash": stash}})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != StatusError || !strings.Contains(result.Summary, "invalid") {
			t.Errorf("unsafe verify stash %q = %+v", stash, result)
		}
	}
	if runner.calls != 0 {
		t.Fatalf("unsafe verify stash reached fcheap, calls=%d", runner.calls)
	}
}

func TestFcheapSaveRejectsUnsafeReturnedStashID(t *testing.T) {
	for _, returnedID := range []string{"--help", "../escape", "safe?query"} {
		runner := &countingRunner{stdout: fmt.Sprintf(`{"id":%q}`, returnedID)}
		f := &Fcheap{tool: tool{bin: "git", run: runner, redact: redact.New(), timeout: time.Second}}
		if _, err := f.Save(context.Background(), t.TempDir(), "bundle", nil, "cortex"); err == nil || !strings.Contains(err.Error(), "invalid stash id") {
			t.Errorf("unsafe returned stash id %q should fail, got %v", returnedID, err)
		}
	}
}

func TestFcheapVerifyRejectsInvalidOrMismatchedReturnedStashID(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   string
	}{
		{name: "invalid", output: `{"id":"--help","files":[]}`, want: "invalid stash identity"},
		{name: "mismatch", output: `{"id":"other","files":[]}`, want: "different stash identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &countingRunner{stdout: tc.output}
			f := &Fcheap{tool: tool{bin: "git", run: runner, redact: redact.New(), timeout: time.Second}}
			result, err := f.Execute(context.Background(), Request{Operation: "verify", Input: map[string]any{"stash": "safe"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != StatusPartial || !strings.Contains(result.Summary, tc.want) {
				t.Fatalf("verify returned id result = %+v", result)
			}
		})
	}
}
