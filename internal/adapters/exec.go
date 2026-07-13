package adapters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// runner executes a CLI tool. It is an interface so tests can inject a fake
// process without spawning real binaries in adapter contract tests.
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
	// configurable max_raw_output_bytes_per_tool cap is NOT applied
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
// a runner, a redactor, a default timeout, and the read-only retry budget.
type tool struct {
	bin     string
	run     runner
	redact  *redact.Redactor
	timeout time.Duration
	retries int // max automatic retries for read-only exec (budget max_auto_retries_per_tool)
}

func newTool(bin string, timeout time.Duration) tool {
	return tool{bin: bin, run: execRunner{}, redact: redact.New(), timeout: timeout, retries: 1}
}

// SetMaxAutoRetries threads budget.max_auto_retries_per_tool into this tool's
// read-only exec path. 0 disables automatic retry; negatives clamp to 0.
// Mutations are unaffected — they run via execOnce, which never retries.
func (t *tool) SetMaxAutoRetries(n int) {
	if n < 0 {
		n = 0
	}
	t.retries = n
}

// exec runs a READ-ONLY idempotent query and returns redacted stdout/stderr.
// A missing binary yields ErrToolMissing. On a transient process/transport
// failure it retries up to budget.max_auto_retries_per_tool —
// safe because query ops are idempotent. A non-zero exit is data, not a
// transient failure, so it is never retried. Mutating ops (fcheap save,
// vecgrep memory remember) must call execOnce to avoid a double write.
func (t tool) exec(ctx context.Context, dir string, args ...string) (stdout, stderr string, exit int, err error) {
	if !binExists(t.bin) {
		return "", "", -1, ErrToolMissing
	}
	attempts := 0
	for {
		attempts++
		stdout, stderr, exit, err = t.execOnce(ctx, dir, args...)
		if err == nil || attempts > t.retries || !retryableExecErr(ctx, err) {
			break
		}
	}
	// Record attempt count and final cause durably: the wrapped error reaches the
	// adapter's unavailable() fact/warning and the kernel's commands.jsonl note.
	if err != nil && attempts > 1 {
		err = fmt.Errorf("failed after %d attempts (retry budget %d); final cause: %w", attempts, t.retries, err)
	}
	return
}

// retryableExecErr classifies a failure as transient and safe to retry
// when the runner itself errored (spawn/pipe/child-timeout) while the
// CALLER's context is still live. A non-zero exit never reaches here — execOnce
// returns it as data with err == nil — so behavioral failures and tool errors
// are never replayed; only infrastructure failures are.
func retryableExecErr(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() == nil
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
// or "" when the binary is missing or reports no version. Best-effort — never
// errors.
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
