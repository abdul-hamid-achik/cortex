package kernel

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/config"
)

// CommandGrant is a trusted-launcher approval for one configured verifier argv
// in one workspace. It lives in the operator config directory, never in the
// repository, so cortex.yaml cannot grant itself execution.
type CommandGrant struct {
	Workspace  string    `json:"workspace"`
	Name       string    `json:"name"`
	Argv       []string  `json:"argv"`
	ArgvDigest string    `json:"argvDigest"`
	GrantedAt  time.Time `json:"grantedAt"`
}

func commandGrantsPath() string {
	return filepath.Join(config.ConfigDir(), "command-grants.json")
}

func argvDigest(argv []string) string {
	h := sha256.New()
	for _, arg := range argv {
		_, _ = h.Write([]byte(arg))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

func loadCommandGrants() []CommandGrant {
	data, err := os.ReadFile(commandGrantsPath())
	if err != nil {
		return nil
	}
	var grants []CommandGrant
	if json.Unmarshal(data, &grants) != nil {
		return nil
	}
	return grants
}

func writeCommandGrants(grants []CommandGrant) error {
	path := commandGrantsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(grants)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (k *Kernel) commandGrantAllows(op string) bool {
	spec, ok := k.cfg.Verifiers[op]
	if !ok || len(spec.Argv) == 0 {
		return false
	}
	wantDigest := argvDigest(spec.Argv)
	workspace := filepath.Clean(k.cfg.Workspace)
	for _, grant := range loadCommandGrants() {
		if filepath.Clean(grant.Workspace) == workspace && grant.Name == op && grant.ArgvDigest == wantDigest {
			return true
		}
	}
	return false
}

// TrustCommandVerifiers writes digest-bound grants for every configured
// verifier in this workspace. Callers must have already shown the argv to a
// human or trusted harness; this function does not print or prompt.
func (k *Kernel) TrustCommandVerifiers() ([]CommandGrant, error) {
	if len(k.cfg.Verifiers) == 0 {
		return nil, fmt.Errorf("no command verifiers configured in this workspace")
	}
	workspace := filepath.Clean(k.cfg.Workspace)
	grants := loadCommandGrants()
	now := k.now().UTC()
	var written []CommandGrant
	for name, spec := range k.cfg.Verifiers {
		if len(spec.Argv) == 0 {
			continue
		}
		grant := CommandGrant{
			Workspace: workspace, Name: name, Argv: append([]string(nil), spec.Argv...),
			ArgvDigest: argvDigest(spec.Argv), GrantedAt: now,
		}
		replaced := false
		for i, existing := range grants {
			if filepath.Clean(existing.Workspace) == workspace && existing.Name == name {
				grants[i] = grant
				replaced = true
				break
			}
		}
		if !replaced {
			grants = append(grants, grant)
		}
		written = append(written, grant)
	}
	if len(written) == 0 {
		return nil, fmt.Errorf("no command verifiers with argv to grant")
	}
	if err := writeCommandGrants(grants); err != nil {
		return nil, err
	}
	return written, nil
}

func (k *Kernel) ConfiguredCommandVerifiers() map[string][]string {
	out := make(map[string][]string, len(k.cfg.Verifiers))
	for name, spec := range k.cfg.Verifiers {
		out[name] = append([]string(nil), spec.Argv...)
	}
	return out
}
