package eval

import (
	"os"
	"testing"
)

// TestMain isolates the scenario harness from the operator's live Cortex state.
// Scenarios build kernels over repo-local temp case stores, and kernel.New
// registers every custom store root in known-stores.json under the state dir;
// without a throwaway CORTEX_HOME each run leaked temp roots into the real
// registry. Mirrors internal/kernel's TestMain.
func TestMain(m *testing.M) {
	for _, k := range []string{"CORTEX_CASES_DIR", "CORTEX_STATE_DIR", "CORTEX_CONFIG_DIR", "CORTEX_CACHE_DIR"} {
		_ = os.Unsetenv(k)
	}
	base, err := os.MkdirTemp("", "cortex-evaltest-")
	if err == nil {
		_ = os.Setenv("CORTEX_HOME", base)
	}
	code := m.Run()
	if base != "" {
		_ = os.RemoveAll(base)
	}
	os.Exit(code)
}
