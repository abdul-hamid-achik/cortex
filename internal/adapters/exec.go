package adapters

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// runner executes a CLI tool. It is an interface so tests can inject a fake
// process without spawning real binaries (SPEC §23.2 adapter contract tests).
type runner interface {
	run(ctx context.Context, dir, bin string, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// execRunner is the production runner backed by os/exec.
type execRunner struct{}

func (execRunner) run(ctx context.Context, dir, bin string, args ...string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
			err = nil // a non-zero exit is data, not a runner failure
		}
	}
	// A generous fixed backstop guards against a runaway process. The per-tool
	// configurable cap (SPEC §7.3 max_raw_output_bytes_per_tool) is NOT applied
	// here or in execOnce: it bounds the raw *retained for the case file*, so it
	// is applied when the raw is stored (kernel.storeRaw). Capping the string the
	// adapter parses would corrupt valid-but-large JSON into an unparseable blob.
	return capBytes(out.Bytes(), rawBackstop), capBytes(errb.Bytes(), rawBackstop), exit, err
}

// rawBackstop is a hard memory guard, independent of the configurable per-tool
// output cap.
const rawBackstop = 4 << 20 // 4 MiB

func capBytes(b []byte, max int) []byte {
	if max > 0 && len(b) > max {
		return append(b[:max:max], []byte("\n…(truncated)")...)
	}
	return b
}

// binExists reports whether a binary is resolvable on PATH.
func binExists(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// tool bundles the shared machinery every CLI adapter needs: the binary name,
// a runner, a redactor, and a default timeout.
type tool struct {
	bin     string
	run     runner
	redact  *redact.Redactor
	timeout time.Duration
}

func newTool(bin string, timeout time.Duration) tool {
	return tool{bin: bin, run: execRunner{}, redact: redact.New(), timeout: timeout}
}

// exec runs a READ-ONLY idempotent query and returns redacted stdout/stderr.
// A missing binary yields ErrToolMissing. On a transient process/transport
// failure it retries ONCE (SPEC §17.3 / budget max_auto_retries_per_tool: 1) —
// safe because query ops are idempotent. A non-zero exit is data, not a
// transient failure, so it is never retried. Mutating ops (fcheap save,
// vecgrep memory remember) must call execOnce to avoid a double write.
func (t tool) exec(ctx context.Context, dir string, args ...string) (stdout, stderr string, exit int, err error) {
	if !binExists(t.bin) {
		return "", "", -1, ErrToolMissing
	}
	stdout, stderr, exit, err = t.execOnce(ctx, dir, args...)
	// Retry once only on a transient runner error and only if the caller's
	// context is still live (don't retry a genuine timeout/cancellation).
	if err != nil && ctx.Err() == nil {
		stdout, stderr, exit, err = t.execOnce(ctx, dir, args...)
	}
	return
}

// execOnce runs the binary exactly once (no retry). Callers must have verified
// the binary exists.
func (t tool) execOnce(ctx context.Context, dir string, args ...string) (stdout, stderr string, exit int, err error) {
	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}
	so, se, code, runErr := t.run.run(ctx, dir, t.bin, args...)
	if runErr != nil {
		return "", "", code, runErr
	}
	// Return the full (rawBackstop-bounded) output so the adapter parses complete
	// JSON. The per-tool storage cap is applied later, when the raw is retained.
	return t.redact.String(string(so)), t.redact.String(string(se)), code, nil
}

// ErrToolMissing indicates the adapter's binary is not on PATH.
var ErrToolMissing = errors.New("binary not found on PATH")

// Version returns the tool's version string (first line of `<bin> --version`),
// or "" when the binary is missing or reports no version (SPEC §14.3 "version
// if known"). Best-effort — never errors.
func (t tool) Version(ctx context.Context) string {
	if !binExists(t.bin) {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	so, _, code, err := t.run.run(ctx, "", t.bin, "--version")
	if err != nil || code != 0 {
		return ""
	}
	return firstLine(string(so))
}

// healthByVersion is a default Health() that runs `<bin> --version` and treats
// a resolvable, non-erroring binary as healthy.
func (t tool) healthByVersion(ctx context.Context) error {
	if !binExists(t.bin) {
		return ErrToolMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, _, err := t.run.run(ctx, "", t.bin, "--version")
	return err
}
