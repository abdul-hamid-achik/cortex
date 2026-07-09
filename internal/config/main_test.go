package config

import (
	"os"
	"testing"
)

// TestMain clears Cortex's env overrides for the whole config test binary. These
// tests assert exact resolved paths, so a developer's exported CORTEX_HOME /
// CORTEX_CASES_DIR / CORTEX_STATE_DIR / CORTEX_CONFIG_DIR / CORTEX_CACHE_DIR would
// otherwise break the assertions. Tests that exercise those vars set them
// explicitly via t.Setenv, which overrides this for their scope.
func TestMain(m *testing.M) {
	for _, k := range []string{"CORTEX_HOME", "CORTEX_CASES_DIR", "CORTEX_STATE_DIR", "CORTEX_CONFIG_DIR", "CORTEX_CACHE_DIR"} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
