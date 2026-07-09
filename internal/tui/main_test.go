package tui

import (
	"os"
	"testing"
)

// TestMain hardens test isolation — see the kernel package's TestMain. The
// per-dir env overrides (CORTEX_CASES_DIR/STATE_DIR/CONFIG_DIR/CACHE_DIR) beat a
// per-test CORTEX_HOME, so clear them for the whole binary or a developer who
// exported one would have these tests write into their real cortex state.
func TestMain(m *testing.M) {
	for _, k := range []string{"CORTEX_CASES_DIR", "CORTEX_STATE_DIR", "CORTEX_CONFIG_DIR", "CORTEX_CACHE_DIR"} {
		_ = os.Unsetenv(k)
	}
	base, err := os.MkdirTemp("", "cortex-tuitest-")
	if err == nil {
		_ = os.Setenv("CORTEX_HOME", base)
	}
	code := m.Run()
	if base != "" {
		_ = os.RemoveAll(base)
	}
	os.Exit(code)
}
