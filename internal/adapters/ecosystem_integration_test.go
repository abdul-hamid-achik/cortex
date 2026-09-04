package adapters

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Run with CORTEX_TEST_CODEMAP_BIN and/or CORTEX_TEST_BOB_BIN pointing to
// freshly built sibling binaries. All writes stay in temporary fixtures.
func TestEcosystemCodemapIdentity(t *testing.T) {
	bin := os.Getenv("CORTEX_TEST_CODEMAP_BIN")
	if bin == "" {
		t.Skip("set CORTEX_TEST_CODEMAP_BIN to test the real producer")
	}
	root := t.TempDir()
	isolateEcosystemConfig(t)
	for name, body := range map[string]string{
		"go.mod": "module fixture\n\ngo 1.25\n",
		"a.go":   "package fixture\ntype A struct{}\nfunc (A) Run() {}\n",
		"b.go":   "package fixture\ntype B struct{}\nfunc (B) Run() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runEcosystemCLI(t, bin, root, "init", "--json")
	runEcosystemCLI(t, bin, root, "index", "--no-embed", "--no-lsp", "--cache=false", "--json")
	var targets []Request
	for _, file := range []string{"a.go", "b.go"} {
		raw := runEcosystemCLI(t, bin, root, "symbol-at", file+":3", "--json")
		var result struct {
			Selector *cmSelector `json:"selector"`
		}
		if err := json.Unmarshal(raw, &result); err != nil || result.Selector == nil {
			t.Fatalf("selector=%s err=%v", raw, err)
		}
		sel := result.Selector
		targets = append(targets, Request{Input: map[string]any{"file": sel.File, "start_line": sel.StartLine, "fqn": sel.FQN, "kind": sel.Kind}})
	}
	adapter := NewCodemap()
	adapter.bin = bin
	query := func(want Status) {
		t.Helper()
		result, err := adapter.Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"dir": root, "targets": targets}})
		if err != nil || result.Status != want {
			t.Fatalf("impact=%+v err=%v", result, err)
		}
		origins := map[int]string{}
		for _, fact := range result.Facts {
			if fact.Kind == "code_graph" && fact.InputIndex != nil && fact.Location != nil {
				origins[*fact.InputIndex] = fact.Location.FQN
			}
		}
		if len(origins) != 2 || origins[0] == origins[1] {
			t.Fatalf("homonyms/provenance lost: %+v", result)
		}
	}
	query(StatusAuthoritative)
	path := filepath.Join(root, "a.go")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("// shifted\n\n"), body...), 0600); err != nil {
		t.Fatal(err)
	}
	query(StatusPartial)
	runEcosystemCLI(t, bin, root, "index", "--no-embed", "--no-lsp", "--cache=false", "--json")
	query(StatusAuthoritative) // Old declaration line still resolves through its FQN.
}

func TestEcosystemBobBatch(t *testing.T) {
	bin := os.Getenv("CORTEX_TEST_BOB_BIN")
	if bin == "" {
		t.Skip("set CORTEX_TEST_BOB_BIN to test the real producer")
	}
	isolateEcosystemConfig(t)
	parent := t.TempDir()
	root := filepath.Join(parent, "fixture")
	runEcosystemCLI(t, bin, parent, "new", "fixture", "--module", "example.com/fixture", "--dir", root, "--write")
	adapter := NewBob()
	adapter.bin = bin
	result, err := adapter.Execute(context.Background(), Request{Operation: "path", Input: map[string]any{"workspace": root, "paths": []string{"internal/cli/root.go", "internal/domain/service.go"}}})
	if err != nil || result.Status != StatusAuthoritative || len(result.Facts) != 2 {
		t.Fatalf("batch=%+v err=%v", result, err)
	}
	if result.Facts[0].Attributes["human_edit_effect"] != "will_conflict" || result.Facts[1].Attributes["human_edit_effect"] != "outside_bob_ownership" {
		t.Fatalf("ownership changed: %+v", result.Facts)
	}
}

func isolateEcosystemConfig(t *testing.T) {
	t.Helper()
	configDir, data := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("CODEMAP_DATA", filepath.Join(data, "codemap"))
	config := filepath.Join(configDir, "codemap.yaml")
	if err := os.WriteFile(config, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMAP_CONFIG", config)
}

func runEcosystemCLI(t *testing.T, bin, dir string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("%s %v: %v: %s", bin, args, err, exit.Stderr)
		}
		t.Fatal(err)
	}
	return out
}
