package mcp

import (
	"context"
	"fmt"
	"os"
	"testing"
)

const stdioHelperEnv = "CORTEX_TEST_MCP_STDIO_HELPER"

// TestMain hardens test isolation — see the kernel package's TestMain. The
// per-dir env overrides (CORTEX_CASES_DIR/STATE_DIR/CONFIG_DIR/CACHE_DIR) beat a
// per-test CORTEX_HOME, so clear them for the whole binary or a developer who
// exported one would have these tests write into their real cortex state.
func TestMain(m *testing.M) {
	if os.Getenv(stdioHelperEnv) == "1" {
		srv, err := NewServerWithProfile(os.Getenv("CORTEX_TEST_MCP_WORKSPACE"), string(ProfileAgent))
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := srv.Run(context.Background()); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	for _, k := range []string{"CORTEX_CASES_DIR", "CORTEX_STATE_DIR", "CORTEX_CONFIG_DIR", "CORTEX_CACHE_DIR"} {
		_ = os.Unsetenv(k)
	}
	base, err := os.MkdirTemp("", "cortex-mcptest-")
	if err == nil {
		_ = os.Setenv("CORTEX_HOME", base)
	}
	code := m.Run()
	if base != "" {
		_ = os.RemoveAll(base)
	}
	os.Exit(code)
}
