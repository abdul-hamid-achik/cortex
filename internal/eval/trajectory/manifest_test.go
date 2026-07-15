package trajectory

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sampleManifestPath(t *testing.T) string {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "evaluations", "terminal-command-regression.yaml"))
}

func TestLoadManifestValidatesPinnedSample(t *testing.T) {
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "terminal-command-regression" || len(manifest.Arms) != 4 || manifest.Arms[0] != ArmRawTools {
		t.Fatalf("sample manifest = %+v", manifest)
	}
	if len(manifest.Oracle.ProtectedPaths) != 2 || manifest.Oracle.ProtectedPaths[0] != "cmd/hello/main_test.go" {
		t.Fatalf("sample oracle protected paths = %v", manifest.Oracle.ProtectedPaths)
	}
	if digest, err := TreeDigest(manifest.RepositoryPath()); err != nil || digest != manifest.Repository.Digest {
		t.Fatalf("sample digest=%s err=%v", digest, err)
	}
}

func TestManifestRejectsDuplicateOracleIDsAcrossCommandAndGlyph(t *testing.T) {
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Oracle.GlyphrunSpecs[0].ID = manifest.Oracle.Commands[0].ID
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate oracle id") {
		t.Fatalf("duplicate command/glyph oracle id error = %v", err)
	}
}

func TestManifestStrictlyRejectsUnknownFutureAndMultipleDocuments(t *testing.T) {
	original, err := os.ReadFile(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unknown field", data: string(original) + "approval: true\n", want: "field approval not found"},
		{name: "future schema", data: strings.Replace(string(original), "schema_version: 1", "schema_version: 2", 1), want: "unsupported trajectory manifest schema"},
		{name: "multiple documents", data: string(original) + "---\nid: second\n", want: "multiple YAML documents"},
		{name: "unsafe fixture path", data: strings.Replace(string(original), "fixture: testrepos/cli-v1", "fixture: ../testrepos/cli-v1", 1), want: "clean repository-relative"},
		{name: "wrong digest", data: strings.Replace(string(original), "sha256:48dbc1efe071654ec5afaf49427b58679cc87d0b86919a0b67d8b56e943a83d9", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1), want: "does not match manifest"},
		{name: "protected path overlaps change", data: strings.Replace(string(original), "  - cmd/hello/main.go", "  - cmd/hello/main_test.go", 1), want: "overlaps allowed change"},
		{name: "relative oracle executable unprotected", data: strings.Replace(string(original), "argv: [go, test, ./...]", "argv: [./cmd/hello/main.go]", 1), want: "must be listed in protected_paths"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "invalid-test.yaml")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := copyFixture(
				filepath.Join(filepath.Dir(sampleManifestPath(t)), "testrepos", "cli-v1"),
				filepath.Join(root, "testrepos", "cli-v1"),
			); err != nil {
				t.Fatal(err)
			}
			specSource := filepath.Join(filepath.Dir(sampleManifestPath(t)), "oracles", "terminal-command-regression", "hello.yml")
			spec, err := os.ReadFile(specSource)
			if err != nil {
				t.Fatal(err)
			}
			specTarget := filepath.Join(root, "oracles", "terminal-command-regression", "hello.yml")
			if err := os.MkdirAll(filepath.Dir(specTarget), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(specTarget, spec, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadManifest(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestLauncherConfigCannotCarryEnvironmentOrApproval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher.yaml")
	if err := os.WriteFile(path, []byte("schema_version: 1\nargv: [trusted-launcher]\nenv: {CORTEX_APPROVE_TRAJECTORY: 1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLauncherConfig(path); err == nil || !strings.Contains(err.Error(), "field env not found") {
		t.Fatalf("launcher config accepted env authority: %v", err)
	}
	if err := (LauncherConfig{SchemaVersion: 1, Argv: []string{""}}).Validate(); err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("empty launcher executable accepted: %v", err)
	}
	if err := (LauncherConfig{SchemaVersion: 1, Argv: []string{"./trusted-launcher"}}).Validate(); err == nil || !strings.Contains(err.Error(), "absolute clean path") {
		t.Fatalf("relative launcher executable accepted: %v", err)
	}
}

func TestLauncherIsResolvedOnceAndBoundToItsDigest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("trajectory execution is unsupported without process-group isolation")
	}
	root := t.TempDir()
	target := filepath.Join(root, "launcher-real")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "launcher-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, provenance, err := resolveLauncher(LauncherConfig{SchemaVersion: 1, Argv: []string{link, "--trusted"}})
	if err != nil {
		t.Fatal(err)
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	resolvedInfo, err := os.Stat(resolved.Argv[0])
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Argv[0] != provenance.ResolvedPath || !os.SameFile(targetInfo, resolvedInfo) || !digestPattern.MatchString(provenance.BinaryDigest) || provenance.Argv[0] != link {
		t.Fatalf("resolved launcher provenance = %+v config=%+v", provenance, resolved)
	}
}

func TestFixtureDigestAndCopyEnforceStreamingBounds(t *testing.T) {
	root := t.TempDir()
	large := filepath.Join(root, "large.bin")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(9); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	limits := fixtureLimits{maxFileBytes: 8, maxTotalBytes: 16, maxEntries: 4, maxPathBytes: 128}
	if _, err := treeDigestWithLimits(root, limits); err == nil || !strings.Contains(err.Error(), "8-byte limit") {
		t.Fatalf("oversized fixture digest error = %v", err)
	}
	if err := copyFixtureWithLimits(root, filepath.Join(t.TempDir(), "copy"), limits); err == nil || !strings.Contains(err.Error(), "8-byte limit") {
		t.Fatalf("oversized fixture copy error = %v", err)
	}

	if err := os.Remove(large); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	limits = fixtureLimits{maxFileBytes: 8, maxTotalBytes: 16, maxEntries: 1, maxPathBytes: 128}
	if _, err := treeDigestWithLimits(root, limits); err == nil || !strings.Contains(err.Error(), "1-entry limit") {
		t.Fatalf("fixture cardinality error = %v", err)
	}
}

func TestTreeDigestRejectsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on windows")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := TreeDigest(root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink fixture accepted: %v", err)
	}
}

func TestTreeDigestRejectsSymlinkRootAndGitMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on windows")
	}
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(parent, "linked")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := TreeDigest(linkRoot); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink root accepted: %v", err)
	}
	if err := os.Mkdir(filepath.Join(realRoot, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := TreeDigest(realRoot); err == nil || !strings.Contains(err.Error(), "git metadata") {
		t.Fatalf("fixture git metadata accepted: %v", err)
	}
}

func TestPathComponentSymlinkIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on windows")
	}
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "oracle.yml"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := validatePathComponents(root, "linked/oracle.yml", false); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink component accepted: %v", err)
	}
}
