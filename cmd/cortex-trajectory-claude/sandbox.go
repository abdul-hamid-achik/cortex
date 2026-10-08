/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

// armRootNames are the per-arm state roots the runner gives the launcher. The
// agent and its Cortex MCP server must use exactly these so no state leaks
// between arms or into the operator's real configuration.
var armRootNames = []string{
	"CORTEX_CONFIG_DIR", "CORTEX_STATE_DIR", "CORTEX_CACHE_DIR", "CORTEX_CASES_DIR",
	"XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
}

const childBasePath = "/usr/bin:/bin:/usr/sbin:/sbin"

// hostEnv is the launcher's own environment.
type hostEnv map[string]string

func newHostEnv(environ []string) hostEnv {
	env := make(hostEnv, len(environ))
	for _, entry := range environ {
		if name, value, ok := strings.Cut(entry, "="); ok {
			env[name] = value
		}
	}
	return env
}

// armRoots returns the per-arm roots present in the launcher environment. The
// cortex arm requires all of them to be absolute clean paths.
func armRoots(host hostEnv, arm trajectory.Arm) (map[string]string, error) {
	roots := map[string]string{}
	for _, name := range armRootNames {
		value := host[name]
		if value == "" {
			continue
		}
		if err := requireCleanAbs(name, value); err != nil {
			return nil, err
		}
		roots[name] = value
	}
	if arm == trajectory.ArmCortex {
		for _, name := range armRootNames {
			if roots[name] == "" {
				return nil, fmt.Errorf("the cortex arm requires %s in the launcher environment", name)
			}
		}
	}
	return roots, nil
}

// sandbox is the private scratch area for one agent run: a fresh HOME and
// TMPDIR, the PATH shim directory, and the MCP config. All of it lives outside
// the workspace and is removed afterwards.
type sandbox struct {
	Root      string
	Home      string
	Tmp       string
	Shim      string
	MCPConfig string
	Env       []string
}

func (s *sandbox) Close() {
	if s != nil && s.Root != "" {
		_ = os.RemoveAll(s.Root) // #nosec G703 -- s.Root is the launcher-created private temp directory
	}
}

// newSandbox builds the isolated execution environment for the arm.
func newSandbox(workspace string, arm trajectory.Arm, opt options, host hostEnv, roots map[string]string) (s *sandbox, err error) {
	root, err := os.MkdirTemp("", "cortex-traj-claude-")
	if err != nil {
		return nil, fmt.Errorf("create private scratch directory: %w", err)
	}
	s = &sandbox{Root: root}
	defer func() {
		if err != nil {
			s.Close()
			s = nil
		}
	}()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return s, err
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return s, err
	}
	if pathWithin(resolvedWorkspace, resolvedRoot) {
		return s, errors.New("private scratch directory would be inside the workspace; set TMPDIR outside it")
	}
	s.Root = resolvedRoot
	s.Home = filepath.Join(resolvedRoot, "home")
	s.Tmp = filepath.Join(resolvedRoot, "tmp")
	s.Shim = filepath.Join(resolvedRoot, "bin")
	s.MCPConfig = filepath.Join(resolvedRoot, "mcp.json")
	for _, dir := range []string{s.Home, s.Tmp, s.Shim} {
		if err = os.Mkdir(dir, 0o700); err != nil {
			return s, err
		}
	}
	exposed := append([]toolSpec(nil), opt.Tools...)
	if arm == trajectory.ArmCortex {
		exposed = append(exposed, toolSpec{Name: "cortex", Path: opt.Cortex})
	}
	for _, tool := range exposed {
		if err = os.Symlink(tool.Path, filepath.Join(s.Shim, tool.Name)); err != nil {
			return s, fmt.Errorf("expose tool %q: %w", tool.Name, err)
		}
	}
	s.Env = childEnvironment(host, s, roots)
	config, err := mcpConfig(arm, opt.Cortex, serverEnvironment(s, roots))
	if err != nil {
		return s, err
	}
	if err = os.WriteFile(s.MCPConfig, config, 0o600); err != nil {
		return s, err
	}
	return s, nil
}

func (s *sandbox) path() string { return s.Shim + ":" + childBasePath }

// childEnvironment is an allowlist, never an inheritance: anything not named
// here (CLAUDECODE, CLAUDE_CODE_*, CORTEX_APPROVE_*, cloud credentials, ...) is
// absent from the agent environment.
func childEnvironment(host hostEnv, s *sandbox, roots map[string]string) []string {
	values := map[string]string{
		"HOME":   s.Home,
		"PATH":   s.path(),
		"TMPDIR": s.Tmp,
	}
	if key := host["ANTHROPIC_API_KEY"]; key != "" {
		values["ANTHROPIC_API_KEY"] = key
	}
	for _, name := range []string{"LANG", "LC_ALL"} {
		if value := host[name]; value != "" {
			values[name] = value
		}
	}
	for name, value := range roots {
		values[name] = value
	}
	return sortedEnviron(values)
}

// serverEnvironment is the explicit environment of the Cortex MCP server.
// CORTEX_APPROVE_COMMANDS is launcher policy, deliberately granted here so the
// repository's configured verifier can run; the agent's own shell never sees it.
func serverEnvironment(s *sandbox, roots map[string]string) map[string]string {
	values := map[string]string{
		"HOME":                    s.Home,
		"PATH":                    s.path(),
		"TMPDIR":                  s.Tmp,
		"CORTEX_APPROVE_COMMANDS": "1",
	}
	for name, value := range roots {
		values[name] = value
	}
	return values
}

func sortedEnviron(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	environ := make([]string, 0, len(names))
	for _, name := range names {
		environ = append(environ, name+"="+values[name])
	}
	return environ
}

type mcpServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

type mcpFile struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

// mcpConfig renders the strict MCP configuration: empty for the raw arm,
// exactly one cortex server for the cortex arm.
func mcpConfig(arm trajectory.Arm, cortexPath string, env map[string]string) ([]byte, error) {
	config := mcpFile{MCPServers: map[string]mcpServer{}}
	if arm == trajectory.ArmCortex {
		if cortexPath == "" {
			return nil, errors.New("cortex arm needs --cortex")
		}
		config.MCPServers["cortex"] = mcpServer{Command: cortexPath, Args: []string{"serve"}, Env: env}
	}
	return json.Marshal(config)
}

// pathWithin reports whether child is parent or lies below it.
func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
