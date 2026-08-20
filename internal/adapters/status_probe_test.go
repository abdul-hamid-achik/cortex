package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// modeRunner returns different canned output for hybrid vs keyword vecgrep search.
type modeRunner struct {
	hybridOut, hybridErr string
	hybridExit           int
	keywordOut           string
	statusOut, statusErr string
	statusExit           int
}

func (m modeRunner) run(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, int, error) {
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "status") {
		return []byte(m.statusOut), []byte(m.statusErr), m.statusExit, nil
	}
	if strings.Contains(joined, "-m keyword") {
		return []byte(m.keywordOut), nil, 0, nil
	}
	return []byte(m.hybridOut), []byte(m.hybridErr), m.hybridExit, nil
}

func TestCodemapStatusReadyAndUnindexed(t *testing.T) {
	ready := `{"project":"cortex","registered":true,"nodes":120,"files":40,"stale":{"changed":0,"new":0,"deleted":0}}`
	c := &Codemap{tool: fakeTool(ready, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "status"})
	if res.Status != StatusAuthoritative {
		t.Fatalf("ready status = %s (%s)", res.Status, res.Summary)
	}
	if res.Facts[0].Attributes["fix"] != "codemap index" {
		t.Errorf("ready fix hint = %q", res.Facts[0].Attributes["fix"])
	}

	missing := `{"project":"cortex","registered":false,"nodes":0,"files":0,"stale":0}`
	c = &Codemap{tool: fakeTool(missing, "", 0)}
	res, _ = c.Execute(context.Background(), Request{Operation: "status"})
	if res.Status != StatusUnavailable {
		t.Fatalf("unindexed status = %s", res.Status)
	}
	if res.Facts[0].Attributes["fix"] != "codemap index" {
		t.Errorf("unindexed fix = %q", res.Facts[0].Attributes["fix"])
	}
}

func TestCodemapStatusStaleIsPartial(t *testing.T) {
	stale := `{"project":"cortex","registered":true,"nodes":10,"files":4,"stale":{"changed":3,"new":0,"deleted":0}}`
	c := &Codemap{tool: fakeTool(stale, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "status"})
	if res.Status != StatusPartial {
		t.Fatalf("stale status = %s", res.Status)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "stale") {
		t.Errorf("want stale warning, got %v", res.Warnings)
	}
}

func TestVecgrepStatusNoIndexAndCorrupt(t *testing.T) {
	v := &Vecgrep{tool: tool{
		bin: "git", run: modeRunner{statusOut: "", statusErr: "not in a vecgrep project", statusExit: 1},
		redact: redact.New(), timeout: time.Second,
	}}
	res, _ := v.Execute(context.Background(), Request{Operation: "status"})
	if res.Status != StatusUnavailable || res.Facts[0].Attributes["fix"] != "vecgrep init && vecgrep index" {
		t.Fatalf("no-project = %+v", res)
	}

	v = &Vecgrep{tool: tool{
		bin: "git", run: modeRunner{statusOut: "", statusErr: "collection not found: chunks", statusExit: 1},
		redact: redact.New(), timeout: time.Second,
	}}
	res, _ = v.Execute(context.Background(), Request{Operation: "status"})
	if res.Status != StatusUnavailable {
		t.Fatalf("corrupt status = %s", res.Status)
	}
	if got := res.Facts[0].Attributes["fix"]; got != "vecgrep reset --force && vecgrep index" {
		t.Errorf("corrupt fix = %q", got)
	}
}

func TestVecgrepHybridRetriesKeywordOnEmbedderFailure(t *testing.T) {
	hybridFail := `{"schema_version":1,"index":{"indexed":true,"fresh":true,"chunks":10},"hits":[]}`
	keywordOK := `{"schema_version":1,"index":{"indexed":true,"fresh":true,"chunks":10},"hits":[
	  {"relative_path":"a.go","start_line":1,"content":"func Foo() error { return nil }","score":0.55}]}`
	v := &Vecgrep{tool: tool{
		bin: "git",
		run: modeRunner{
			hybridOut: hybridFail, hybridErr: "embedding profile is missing", hybridExit: 1,
			keywordOut: keywordOK,
		},
		redact: redact.New(), timeout: time.Second,
	}}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{
		"query": "Foo", "mode": "hybrid",
	}})
	if res.Status != StatusAuthoritative || len(res.Facts) != 1 {
		t.Fatalf("keyword retry should succeed authoritatively, got %+v", res)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "retried as keyword") {
		t.Errorf("want keyword-retry warning, got %v", res.Warnings)
	}
}

func TestVecgrepHybridDoesNotRetryOnMissingIndex(t *testing.T) {
	calls := 0
	r := countingArgsRunner{onCall: func(args []string) (string, string, int) {
		calls++
		return `{"schema_version":1,"index":{"indexed":false,"fresh":false,"chunks":0},"hits":[]}`, "", 0
	}}
	v := &Vecgrep{tool: tool{bin: "git", run: &r, redact: redact.New(), timeout: time.Second}}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{
		"query": "Foo", "mode": "hybrid",
	}})
	if res.Status != StatusUnavailable {
		t.Fatalf("no-index should stay unavailable, got %s", res.Status)
	}
	if calls != 1 {
		t.Fatalf("missing index must not keyword-retry, calls=%d", calls)
	}
}

type countingArgsRunner struct {
	onCall func(args []string) (stdout, stderr string, exit int)
	n      int
}

func (c *countingArgsRunner) run(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, int, error) {
	c.n++
	out, err, exit := c.onCall(args)
	return []byte(out), []byte(err), exit, nil
}

func TestTimedOutIsNotNeedsIndex(t *testing.T) {
	res := timedOut("vecgrep", "search", 30*time.Second)
	if res.Status != StatusPartial {
		t.Fatalf("status = %s", res.Status)
	}
	if !IsTimeoutResult(res) {
		t.Fatal("expected IsTimeoutResult")
	}
	if res.Facts[0].Attributes["index"] != "timeout" {
		t.Fatalf("index attr = %q", res.Facts[0].Attributes["index"])
	}
}

func TestFailExecMapsDeadline(t *testing.T) {
	res := failExec("codemap", "find", context.DeadlineExceeded, 20*time.Second)
	if !IsTimeoutResult(res) {
		t.Fatalf("want timeout classification, got %+v", res)
	}
	res = failExec("codemap", "find", errors.New("spawn failed"), 20*time.Second)
	if res.Status != StatusUnavailable || IsTimeoutResult(res) {
		t.Fatalf("non-timeout should be unavailable, got %+v", res)
	}
}

func TestVecgrepHybridRetriesKeywordOnTimeout(t *testing.T) {
	keywordOK := `{"schema_version":1,"index":{"indexed":true,"fresh":true,"chunks":10},"hits":[
	  {"relative_path":"a.go","start_line":1,"content":"func Foo() error { return nil }","score":0.55}]}`
	v := &Vecgrep{tool: tool{
		bin: "git", run: &timeoutThenKeywordRunner{keywordOut: keywordOK},
		redact: redact.New(), timeout: time.Second,
	}}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{
		"query": "Foo", "mode": "hybrid",
	}})
	if res.Status != StatusAuthoritative || len(res.Facts) != 1 {
		t.Fatalf("keyword retry after timeout should succeed, got %+v", res)
	}
}

type timeoutThenKeywordRunner struct {
	keywordOut string
	n          int
}

func (t *timeoutThenKeywordRunner) run(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, int, error) {
	t.n++
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-m keyword") {
		return []byte(t.keywordOut), nil, 0, nil
	}
	return nil, nil, -1, context.DeadlineExceeded
}
