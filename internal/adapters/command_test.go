package adapters

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

type commandRunner struct {
	bin, dir string
	args     []string
	stdout   string
	stderr   string
	exit     int
}

func (r *commandRunner) run(_ context.Context, dir, bin string, args ...string) ([]byte, []byte, int, error) {
	r.dir, r.bin, r.args = dir, bin, append([]string(nil), args...)
	return []byte(r.stdout), []byte(r.stderr), r.exit, nil
}

func TestCommandVerifierUsesConfiguredArgvWithoutShell(t *testing.T) {
	r := &commandRunner{stdout: "ok\n"}
	c := NewCommandVerifier(map[string]CommandSpec{
		"unit": {Argv: []string{"git", "status", "--short; touch /tmp/nope"}, Kind: "unit_test", Surface: "code", Timeout: time.Second},
	})
	c.run, c.red = r, redact.New()
	res, err := c.Execute(context.Background(), Request{Operation: "unit", Input: map[string]any{"dir": t.TempDir(), "argv": []string{"sh", "-c", "bad"}}})
	if err != nil || res.Verdict != VerdictPassed {
		t.Fatalf("command verifier = %+v, %v", res, err)
	}
	if r.bin != "git" || len(r.args) != 2 || r.args[1] != "--short; touch /tmp/nope" {
		t.Fatalf("configured argv was not passed literally: bin=%q args=%q", r.bin, r.args)
	}
}

func TestCommandVerifierRecordsFailureAndRedacts(t *testing.T) {
	r := &commandRunner{stderr: "TOKEN=supersecretvalue\n", exit: 2}
	c := NewCommandVerifier(map[string]CommandSpec{
		"lint": {Argv: []string{"git", "status"}, Kind: "lint", Surface: "code", Timeout: time.Second},
	})
	c.run, c.red = r, redact.New()
	res, err := c.Execute(context.Background(), Request{Operation: "lint", Input: map[string]any{"dir": t.TempDir()}})
	if err != nil || res.Verdict != VerdictFailed || res.Status != StatusAuthoritative {
		t.Fatalf("failed command = %+v, %v", res, err)
	}
	if strings.Contains(res.Raw, "supersecretvalue") || !strings.Contains(res.Raw, "«redacted»") {
		t.Fatalf("raw output was not redacted: %q", res.Raw)
	}
}

func TestCommandVerifierRejectsUnknownName(t *testing.T) {
	c := NewCommandVerifier(nil)
	res, _ := c.Execute(context.Background(), Request{Operation: "arbitrary"})
	if res.Status != StatusError {
		t.Fatalf("unknown verifier status = %s", res.Status)
	}
}
