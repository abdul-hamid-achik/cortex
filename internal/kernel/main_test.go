package kernel

import (
	"os"
	"testing"
)

// TestMain hardens test isolation. A developer who exported CORTEX_CASES_DIR /
// CORTEX_STATE_DIR / CORTEX_CONFIG_DIR / CORTEX_CACHE_DIR (the repo's own
// CLAUDE.md suggests doing so) would otherwise have these tests write real case
// files into their live cortex state — those per-dir overrides beat the per-test
// CORTEX_HOME. Clear them for the whole test binary and default CORTEX_HOME to a
// throwaway dir; individual tests still set their own CORTEX_HOME as needed.
func TestMain(m *testing.M) {
	for _, k := range []string{"CORTEX_CASES_DIR", "CORTEX_STATE_DIR", "CORTEX_CONFIG_DIR", "CORTEX_CACHE_DIR"} {
		_ = os.Unsetenv(k)
	}
	base, err := os.MkdirTemp("", "cortex-kerneltest-")
	if err == nil {
		_ = os.Setenv("CORTEX_HOME", base)
	}
	code := m.Run()
	if base != "" {
		_ = os.RemoveAll(base)
	}
	os.Exit(code)
}
