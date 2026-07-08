package adapters

import (
	"context"
	"fmt"
	"time"
)

// Tvault adapts the tvault CLI as an execution boundary, not a content provider
// (SPEC §11.3, §12.7). It answers only the permitted model-visible questions —
// is a project available, which secret NAMES exist, is injection granted — and
// never emits secret values, previews, or environment dumps.
type Tvault struct{ tool }

// NewTvault builds a tvault adapter.
func NewTvault() *Tvault { return &Tvault{tool: newTool("tvault", 15*time.Second)} }

func (t *Tvault) Name() string { return "tvault" }

func (t *Tvault) Capabilities() []Capability { return []Capability{CapabilitySecrets} }

// Health runs `tvault doctor --json` (read-only, never unlocks the vault).
func (t *Tvault) Health(ctx context.Context) error {
	if !binExists(t.bin) {
		return ErrToolMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	_, _, _, err := t.run.run(ctx, "", t.bin, "doctor", "--json")
	return err
}

// Execute routes tvault operations. Every branch returns capability metadata
// only — no secret values ever cross this boundary.
func (t *Tvault) Execute(ctx context.Context, req Request) (Result, error) {
	if !binExists(t.bin) {
		return unavailable("tvault", req.Operation, "not on PATH"), nil
	}
	switch req.Operation {
	case "availability", "":
		return t.availability(ctx, req.Str("project"))
	case "list_keys":
		return t.listKeys(ctx, req.Str("project"))
	default:
		return Result{Tool: "tvault", Operation: req.Operation, Status: StatusError,
			Summary: "unknown tvault operation: " + req.Operation}, nil
	}
}

type tvProject struct {
	Name string `json:"name"`
}

// availability reports whether a project exists in the vault (a permitted,
// non-sensitive question). It decodes flexibly since the exact JSON envelope is
// version-dependent, falling back to a substring check.
func (t *Tvault) availability(ctx context.Context, project string) (Result, error) {
	stdout, stderr, code, err := t.namesOnly(ctx, "projects", "list", "--json")
	if err != nil {
		return unavailable("tvault", "availability", err.Error()), nil
	}
	if vaultLocked(code, stdout, stderr) {
		return unavailable("tvault", "availability", "vault is locked — cannot enumerate projects without unlocking"), nil
	}
	if code != 0 {
		return degraded("tvault", "availability", stdout, stderr, code), nil
	}
	present := project != "" && containsProject(stdout, project)
	claim := fmt.Sprintf("tvault project %q available: %t", project, present)
	if project == "" {
		claim = "tvault is reachable; vault projects listed (names only)"
	}
	return Result{
		Tool: "tvault", Operation: "availability", Status: StatusAuthoritative,
		Summary: claim,
		// A capability fact carries no secret material; confidence is high.
		Facts: []Fact{{Kind: "code_location", Claim: claim, Confidence: "high"}},
		Raw:   stdout, // project NAMES only — no values
	}, nil
}

// listKeys returns secret KEY NAMES for a project (metadata only, SPEC §12.7).
func (t *Tvault) listKeys(ctx context.Context, project string) (Result, error) {
	if project == "" {
		return Result{Tool: "tvault", Operation: "list_keys", Status: StatusError, Summary: "list_keys needs a project"}, nil
	}
	stdout, stderr, code, err := t.namesOnly(ctx, "list", "-p", project, "--json")
	if err != nil {
		return unavailable("tvault", "list_keys", err.Error()), nil
	}
	if vaultLocked(code, stdout, stderr) {
		return unavailable("tvault", "list_keys", "vault is locked — cannot list key names without unlocking"), nil
	}
	if code != 0 {
		return degraded("tvault", "list_keys", stdout, stderr, code), nil
	}
	return Result{
		Tool: "tvault", Operation: "list_keys", Status: StatusAuthoritative,
		Summary: fmt.Sprintf("listed secret key names for project %q (names only, no values)", project),
		Facts: []Fact{{Kind: "code_location", Confidence: "high", Sensitive: true,
			Claim: fmt.Sprintf("project %q has the following secret key names available for scoped injection", project)}},
		Raw: stdout, // KEY NAMES only
	}, nil
}

// namesOnly runs a tvault listing with --names-only (tvault ≥0.16: lock-free,
// value-free — it enumerates names without unlocking the vault). An older binary
// rejects the unknown flag at parse time (non-zero exit, "unknown flag" on
// stderr), so we retry the plain form, which works when a passphrase/agent is
// present and otherwise degrades exactly as before. Names-only output is shape-
// identical to the plain form, so no parse change is needed.
func (t *Tvault) namesOnly(ctx context.Context, args ...string) (stdout, stderr string, code int, err error) {
	withFlag := append(append([]string{}, args...), "--names-only")
	stdout, stderr, code, err = t.exec(ctx, "", withFlag...)
	if err == nil && code != 0 && (containsFold(stderr, "names-only") || containsFold(stderr, "unknown flag")) {
		return t.exec(ctx, "", args...)
	}
	return stdout, stderr, code, err
}

// vaultLocked recognizes tvault ≥0.16's deterministic non-interactive locked
// signal (exit 3 + {error:vault_locked}) so a locked vault is an honest
// "unavailable", distinct from a genuine error (SPEC §11.4). The substring scan
// is gated on a NON-success exit: on a successful listing (exit 0) stdout is the
// legitimate project/key NAMES, which could themselves contain "vault_locked".
func vaultLocked(code int, stdout, stderr string) bool {
	if code == 0 {
		return false
	}
	return code == 3 || containsFold(stdout, "vault_locked") || containsFold(stderr, "vault_locked")
}

// containsProject checks whether the project name appears in the listing. It is
// a deliberately loose match over already-redacted, values-free output.
func containsProject(listing, project string) bool {
	var ps []tvProject
	if err := decodeJSON(listing, &ps); err == nil {
		for _, p := range ps {
			if p.Name == project {
				return true
			}
		}
		return false
	}
	// Fall back to substring when the shape isn't an array of {name}.
	return containsFold(listing, project)
}

func containsFold(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexFold(haystack, needle) >= 0
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
