/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCLISetupJSON(t *testing.T) {
	ws := cliRepo(t)
	out, err := runCLI(t, "-C", ws, "--json", "setup")
	if err != nil {
		t.Fatalf("setup: %v (%s)", err, out)
	}
	var rep map[string]any
	if e := json.Unmarshal([]byte(out), &rep); e != nil {
		t.Fatalf("setup --json not JSON: %s", out)
	}
	if rep["isRepo"] != true {
		t.Errorf("setup should report isRepo=true, got: %v", rep)
	}
	tools, ok := rep["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("setup should report a non-empty tools list, got: %v", rep)
	}
}

func TestCLISetupHumanViewSuggestsInitWithoutConfig(t *testing.T) {
	ws := cliRepo(t) // no cortex.yaml
	out, err := runCLI(t, "-C", ws, "setup")
	if err != nil {
		t.Fatalf("setup: %v (%s)", err, out)
	}
	if !strings.Contains(out, "cortex init") {
		t.Errorf("setup without a cortex.yaml should suggest `cortex init`, got:\n%s", out)
	}
}
