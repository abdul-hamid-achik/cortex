/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIInitDetectsGoAndWritesConfig(t *testing.T) {
	ws := cliRepo(t)
	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module example.com/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "-C", ws, "--json", "init")
	if err != nil {
		t.Fatalf("init: %v (%s)", err, out)
	}
	var res map[string]any
	if e := json.Unmarshal([]byte(out), &res); e != nil {
		t.Fatalf("init --json not JSON: %s", out)
	}
	if res["created"] != true || res["ok"] != true {
		t.Fatalf("init should create a config: %v", res)
	}

	data, err := os.ReadFile(filepath.Join(ws, "cortex.yaml"))
	if err != nil {
		t.Fatalf("cortex.yaml not written: %v", err)
	}
	if !strings.Contains(string(data), `"go", "test", "./..."`) || !strings.Contains(string(data), "unit_test") {
		t.Errorf("generated config missing the go test verifier:\n%s", data)
	}
}

func TestCLIInitHumanViewShowsNextSteps(t *testing.T) {
	ws := cliRepo(t)
	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module example.com/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "-C", ws, "init")
	if err != nil {
		t.Fatalf("init: %v (%s)", err, out)
	}
	if !strings.Contains(out, "CORTEX_APPROVE_COMMANDS=1") || !strings.Contains(out, "command:unit") {
		t.Errorf("human init should explain approval and the verifier name, got:\n%s", out)
	}
}

func TestCLIInitRefusesExistingWithoutForce(t *testing.T) {
	ws := cliRepo(t)
	original := "budget:\n  max_investigation_rounds: 4\n"
	if err := os.WriteFile(filepath.Join(ws, "cortex.yaml"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "-C", ws, "--json", "init")
	if err != nil {
		t.Fatalf("init: %v (%s)", err, out)
	}
	var res map[string]any
	_ = json.Unmarshal([]byte(out), &res)
	if res["created"] != false || res["existed"] != true {
		t.Fatalf("init should refuse to overwrite: %v", res)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "cortex.yaml"))
	if string(data) != original {
		t.Errorf("existing config was clobbered:\n%s", data)
	}

	out2, err := runCLI(t, "-C", ws, "--json", "init", "--force")
	if err != nil {
		t.Fatalf("init --force: %v (%s)", err, out2)
	}
	var res2 map[string]any
	_ = json.Unmarshal([]byte(out2), &res2)
	if res2["created"] != true {
		t.Fatalf("init --force should write: %v", res2)
	}
}
