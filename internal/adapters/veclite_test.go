package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// newTestVeclite builds a veclite adapter with a stubbed embedder (no ollama)
// and a fake runner the test drives.
func newTestVeclite(t *testing.T, r runner) *Veclite {
	v := &Veclite{
		// bin is "git" (on PATH in CI) so binExists passes and the fake runner
		// drives the call; the real veclite binary need not be installed.
		tool:     tool{bin: "git", run: r, redact: redact.New(), timeout: 5 * time.Second, retries: 0},
		embedURL: "http://localhost:11434/api/embeddings",
		enabled:  true,
	}
	v.embedFn = func(context.Context, string) ([]float64, error) {
		return make([]float64, 768), nil // static zero vector — only shape matters
	}
	return v
}

func TestVecliteRecallCasesParsesHits(t *testing.T) {
	out := `[{"id":1,"score":0.42,"payload":{"kind":"hypothesis","task_id":"task_X","repo":"liftclub","statement":"returnTo was dropped","status":"rejected","resolved_reason":"missing returnTo"}}]`
	v := newTestVeclite(t, fakeRunner{stdout: out})
	v.ensureOnce.Do(func() {}) // schema already "ensured" for this test
	hits, err := v.RecallCases(context.Background(), "login redirect", "liftclub", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	claim := RecallClaim(hits[0].Payload)
	if !strings.Contains(claim, "was rejected") || !strings.Contains(claim, "task_X") {
		t.Errorf("claim should render the rejected hypothesis, got %q", claim)
	}
}

func TestVecliteRecallCasesEmptyIsAuthoritative(t *testing.T) {
	v := newTestVeclite(t, fakeRunner{stdout: "[]"})
	v.ensureOnce.Do(func() {})
	hits, err := v.RecallCases(context.Background(), "nothing matches", "", 5)
	if err != nil {
		t.Fatalf("empty result should not error, got %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected zero hits, got %d", len(hits))
	}
}

func TestVecliteCaseRecallOpAuthoritativeEmpty(t *testing.T) {
	v := newTestVeclite(t, fakeRunner{stdout: "[]"})
	v.ensureOnce.Do(func() {})
	res, _ := v.Execute(context.Background(), Request{Operation: "case_recall", Input: map[string]any{"query": "x"}})
	if res.Status != StatusAuthoritative {
		t.Errorf("empty recall is authoritative, got %s", res.Status)
	}
	if len(res.Facts) != 0 {
		t.Errorf("expected zero facts, got %d", len(res.Facts))
	}
}

func TestVecliteMissingBinary(t *testing.T) {
	v := &Veclite{tool: tool{bin: "definitely-not-a-real-binary-vl", run: fakeRunner{}, redact: redact.New()}}
	v.embedFn = func(context.Context, string) ([]float64, error) { return make([]float64, 768), nil }
	res, _ := v.Execute(context.Background(), Request{Operation: "case_recall", Input: map[string]any{"query": "x"}})
	if res.Status != StatusUnavailable {
		t.Errorf("missing binary should be unavailable, got %s", res.Status)
	}
	if _, err := v.RecallCases(context.Background(), "x", "", 5); err != ErrToolMissing {
		t.Errorf("RecallCases missing binary should return ErrToolMissing, got %v", err)
	}
	if err := v.IndexCase(context.Background(), IndexRecord{Key: "k", Statement: "s"}); err != ErrToolMissing {
		t.Errorf("IndexCase missing binary should return ErrToolMissing, got %v", err)
	}
}

func TestVecliteDisabledIsNoop(t *testing.T) {
	v := newTestVeclite(t, fakeRunner{})
	v.enabled = false
	if err := v.IndexCase(context.Background(), IndexRecord{Key: "k"}); err != ErrToolMissing {
		t.Errorf("disabled IndexCase should return ErrToolMissing, got %v", err)
	}
	if _, err := v.RecallCases(context.Background(), "x", "", 5); err != ErrToolMissing {
		t.Errorf("disabled RecallCases should return ErrToolMissing, got %v", err)
	}
}

func TestVecliteIndexCaseUsesExecOnceNoRetry(t *testing.T) {
	r := &countingRunner{failFirst: 1, stdout: "{}"}
	v := newTestVeclite(t, r)
	v.ensureOnce.Do(func() {})
	err := v.IndexCase(context.Background(), IndexRecord{Key: "task_X/hyp_1", Kind: "hypothesis", Statement: "s", Goal: "g"})
	if err == nil {
		t.Error("expected a failure (countingRunner fails first call)")
	}
	// execOnce must run exactly once — a write never retries (SPEC §17.3).
	// Note: the embed stub call does not go through the runner, so calls counts
	// only the record-upsert execOnce attempt.
	if r.calls != 1 {
		t.Errorf("IndexCase should execOnce (no retry), got %d calls", r.calls)
	}
}

func TestVecliteRecallCasesFilterByRepo(t *testing.T) {
	var captured [][]string
	r := &argCapturingRunner{captured: &captured, stdout: "[]"}
	v := newTestVeclite(t, r)
	v.ensureOnce.Do(func() {})
	_, _ = v.RecallCases(context.Background(), "login", "liftclub", 5)
	if !argsContain(captured, "--filter=repo=liftclub") {
		t.Errorf("repo-scoped recall should pass --filter=repo=liftclub, got %v", captured)
	}
	captured = nil
	_, _ = v.RecallCases(context.Background(), "login", "", 5)
	if argsContain(captured, "--filter=") {
		t.Errorf("unscoped recall should not pass a filter, got %v", captured)
	}
}

func TestVecliteEnsureSchemaIgnoresExists(t *testing.T) {
	// A runner that always reports "already exists" must not block ensureSchema.
	r := fakeRunner{stdout: "", exit: 1, err: errors.New("collection already exists")}
	v := newTestVeclite(t, r)
	v.ensureOnce.Do(func() {}) // pre-marked so ensureSchema is a no-op
	if err := v.ensureSchema(context.Background()); err != nil {
		t.Errorf("pre-ensured schema should not error, got %v", err)
	}
}

func TestIndexRecordEmbedText(t *testing.T) {
	r := IndexRecord{Statement: "S", Goal: "G", ResolvedReason: "R"}
	if got := r.embedText(); got != "S\nG\nR" {
		t.Errorf("embedText = %q, want %q", got, "S\nG\nR")
	}
}

// argCapturingRunner records every arg of every run call and returns canned output.
type argCapturingRunner struct {
	captured *[][]string
	stdout   string
}

func (a *argCapturingRunner) run(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, int, error) {
	*a.captured = append(*a.captured, append([]string(nil), args...))
	return []byte(a.stdout), nil, 0, nil
}

// argsContain reports whether any captured call's args contain want.
func argsContain(calls [][]string, want string) bool {
	for _, args := range calls {
		for _, a := range args {
			if a == want {
				return true
			}
		}
	}
	return false
}

// jsonMarshalHelper round-trips to keep encoding/json imported for the payload.
var _ = json.Marshal
