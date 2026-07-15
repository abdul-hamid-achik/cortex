package trajectory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

type ProcessKind string

const (
	ProcessLauncher ProcessKind = "launcher"
	ProcessOracle   ProcessKind = "oracle"
)

type ProcessRequest struct {
	Kind        ProcessKind
	Arm         Arm
	ID          string
	Argv        []string
	Dir         string
	Stdin       []byte
	Timeout     time.Duration
	MaxStdout   int
	MaxStderr   int
	Environment map[string]string
	// ExpectedBinaryDigest binds real execution to the executable identity
	// recorded before the arm or oracle invocation.
	ExpectedBinaryDigest string
}

type ProcessResult struct {
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
	ExitCode        int
	TimedOut        bool
	Canceled        bool
	Err             error
}

type ProcessRunner interface {
	Run(context.Context, ProcessRequest) ProcessResult
}

type ExecProcessRunner struct{}

var errProcessIsolationUnavailable = errors.New("trajectory process-group isolation is unavailable on this platform")

func (ExecProcessRunner) Run(ctx context.Context, request ProcessRequest) ProcessResult {
	if !processIsolationSupported() {
		return ProcessResult{ExitCode: -1, Err: errProcessIsolationUnavailable}
	}
	if len(request.Argv) == 0 {
		return ProcessResult{ExitCode: -1, Err: errors.New("process argv is empty")}
	}
	if request.ExpectedBinaryDigest != "" {
		digest, err := regularFileDigest(request.Argv[0])
		if err != nil {
			return ProcessResult{ExitCode: -1, Err: fmt.Errorf("verify process executable: %w", err)}
		}
		if digest != request.ExpectedBinaryDigest {
			return ProcessResult{ExitCode: -1, Err: errors.New("process executable digest changed before execution")}
		}
	}
	processCtx := ctx
	cancel := func() {}
	if request.Timeout > 0 {
		processCtx, cancel = context.WithTimeout(ctx, request.Timeout)
	}
	defer cancel()
	stdout := &boundedBuffer{limit: request.MaxStdout}
	stderr := &boundedBuffer{limit: request.MaxStderr}
	cmd := exec.CommandContext(processCtx, request.Argv[0], request.Argv[1:]...)
	cmd.Dir = request.Dir
	cmd.Env = childEnvironment(request.Kind, os.Environ(), request.Environment)
	configureProcessGroup(cmd)
	cmd.Stdin = bytes.NewReader(request.Stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	result := ProcessResult{
		Stdout: stdout.Bytes(), Stderr: stderr.Bytes(),
		StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated,
		ExitCode: 0, Err: err,
	}
	if processCtx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
	} else if processCtx.Err() == context.Canceled {
		result.Canceled = true
	}
	if err != nil {
		result.ExitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		}
	}
	return result
}

func childEnvironment(kind ProcessKind, inherited []string, overrides map[string]string) []string {
	values := map[string]string{}
	redactor := redact.New()
	for _, entry := range inherited {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "CORTEX_") {
			continue
		}
		if kind == ProcessOracle && (secretEnvironmentName(upper) || redactor.Detected(value)) {
			continue
		}
		values[name] = value
	}
	for name, value := range overrides {
		values[name] = value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		filtered = append(filtered, name+"="+values[name])
	}
	return filtered
}

func secretEnvironmentName(name string) bool {
	for _, marker := range []string{
		"TOKEN", "SECRET", "PASSWORD", "PASSWD", "API_KEY", "PRIVATE_KEY",
		"ACCESS_KEY", "CREDENTIAL", "COOKIE", "AUTH",
	} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	for _, prefix := range []string{"AWS_", "GH_", "GITHUB_", "OPENAI_", "ANTHROPIC_", "STRIPE_", "SLACK_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	written := len(data)
	remaining := b.limit - b.buf.Len()
	if b.limit < 1 {
		b.truncated = len(data) > 0
		return written, nil
	}
	if remaining > 0 {
		keep := len(data)
		if keep > remaining {
			keep = remaining
		}
		_, _ = b.buf.Write(data[:keep])
	}
	if len(data) > remaining {
		b.truncated = true
	}
	return written, nil
}

func (b *boundedBuffer) Bytes() []byte {
	return append([]byte(nil), b.buf.Bytes()...)
}
