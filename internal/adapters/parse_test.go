package adapters

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// fakeTool builds a tool that runs a canned response instead of a real binary.
// bin="git" is used only so binExists() passes on every dev machine and in CI;
// the fakeRunner ignores the bin and returns the fixture, so these tests
// exercise the adapters' JSON PARSING without needing the real tools.
func fakeTool(stdout, stderr string, exit int) tool {
	return tool{bin: "git", run: fakeRunner{stdout: stdout, stderr: stderr, exit: exit},
		redact: redact.New(), timeout: time.Second}
}

func factClaims(r Result) string {
	var b strings.Builder
	for _, f := range r.Facts {
		b.WriteString(f.Claim)
		b.WriteString(" | ")
	}
	return b.String()
}

// ---- codemap ----

func TestCodemapImpactParse(t *testing.T) {
	fixture := `{"symbol":"HandleCallback","found":true,"resolution":"precise",
	  "locations":[{"file":"src/auth/callback.go","start_line":42,"end_line":61,"symbol":"HandleCallback"}],
	  "direct_callers":[{"symbol":"Router"}],
	  "blast_radius":[{"symbol":"A"},{"symbol":"B"},{"symbol":"C"}],
	  "tests":[{"symbol":"TestCallback"}],"untested":false}`
	c := &Codemap{tool: fakeTool(fixture, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"symbol": "HandleCallback"}})
	if res.Status != StatusAuthoritative {
		t.Fatalf("status = %s", res.Status)
	}
	claims := factClaims(res)
	if !strings.Contains(claims, "3 symbols") || !strings.Contains(claims, "1 test") {
		t.Errorf("impact facts should report blast radius + tests, got: %s", claims)
	}
	// precise resolution → high confidence.
	if res.Facts[0].Confidence != "high" {
		t.Errorf("precise resolution should be high confidence, got %s", res.Facts[0].Confidence)
	}
}

func TestCodemapImpactNameBasedIsMediumConfidence(t *testing.T) {
	fixture := `{"symbol":"Foo","found":true,"resolution":"name","blast_radius":[{"symbol":"X"}],"tests":[]}`
	c := &Codemap{tool: fakeTool(fixture, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"symbol": "Foo"}})
	if res.Facts[0].Confidence != "medium" {
		t.Errorf("name-based resolution should be medium confidence, got %s", res.Facts[0].Confidence)
	}
}

func TestCodemapReviewUnindexedIsPartial(t *testing.T) {
	fixture := `{"indexed":false,"is_repo":true,"changed_files":[{"path":"a.go","status":"M","symbols":0}],
	  "changed_symbols":[],"blast_radius":[],"covering_tests":[],"note":"project not indexed"}`
	c := &Codemap{tool: fakeTool(fixture, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "review"})
	if res.Status != StatusPartial {
		t.Errorf("unindexed review should be partial, got %s", res.Status)
	}
	if !strings.Contains(factClaims(res), "not indexed") {
		t.Errorf("review should note it's not indexed, got: %s", factClaims(res))
	}
}

func TestCodemapReviewGoldenV1(t *testing.T) {
	fixture, err := os.ReadFile("testdata/codemap.review.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	res, _ := (&Codemap{tool: fakeTool(string(fixture), "", 0)}).Execute(context.Background(), Request{Operation: "review"})
	if res.Status != StatusPartial || !strings.Contains(strings.Join(res.Warnings, " "), "stale") {
		t.Fatalf("canonical stale v1 review should be parsed but remain partial, got %s %v", res.Status, res.Warnings)
	}
	if !strings.Contains(res.Summary, "1 file") || !strings.Contains(res.Summary, "1 symbol") || !strings.Contains(res.Summary, "1 covering test") {
		t.Errorf("canonical schema v1 fields were not parsed: %q", res.Summary)
	}
}

func TestCodemapReviewUnsupportedSchemaVersion(t *testing.T) {
	fixture := `{"schema_version":2,"indexed":true,"is_repo":true,"changed_files":[{"path":"a.go","status":"M","symbols":1}],"changed_symbols":[{"symbol":"Run"}],"covering_tests":[{"symbol":"TestRun"}]}`
	res, _ := (&Codemap{tool: fakeTool(fixture, "", 0)}).Execute(context.Background(), Request{Operation: "review"})
	if res.Status != StatusPartial {
		t.Fatalf("unsupported schema v2 must be partial, never authoritative; got %s", res.Status)
	}
	if len(res.Facts) != 0 {
		t.Errorf("unsupported schema v2 must not produce review facts, got %d", len(res.Facts))
	}
	joined := res.Summary + " " + strings.Join(res.Warnings, " ")
	if !strings.Contains(joined, "inconclusive") || !strings.Contains(joined, "schema_version 2") {
		t.Errorf("unsupported schema warning must be explicit, got %q", joined)
	}
}

func TestCodemapReviewLegacyNameAndPathAliasesRemainVisible(t *testing.T) {
	fixture := `{"indexed":true,"is_repo":true,"changed_files":[{"path":"a.go","status":"M","symbols":1}],"changed_symbols":[{"name":"Run","kind":"function","path":"a.go","start_line":5,"end_line":7}],"blast_radius":[],"covering_tests":[],"untested_symbols":[],"stale":false}`
	res, _ := (&Codemap{tool: fakeTool(fixture, "", 0)}).Execute(context.Background(), Request{Operation: "review"})
	if res.Status != StatusAuthoritative || !strings.Contains(res.Summary, "1 symbol") {
		t.Fatalf("legacy name/path aliases were silently dropped: %s %q", res.Status, res.Summary)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "exported symbol") {
		t.Fatalf("legacy aliased symbol did not reach public-contract checks: %v", res.Warnings)
	}
}

func TestCodemapReviewRejectsExplicitLegacyAndMalformedV1(t *testing.T) {
	fixtures := []string{
		`{"schema_version":0,"indexed":true}`,
		`{"schema_version":null,"indexed":true}`,
		`{"schemaVersion":1,"indexed":true}`,
		`{"schema_version":1,"project":"x","mode":"working","depth":3,"is_repo":true,"indexed":true,"changed_files":[],"changed_symbols":[],"blast_radius":[],"covering_tests":[],"stale":false}`,
		`{"schema_version":1,"project":"x","mode":"working","depth":3,"is_repo":true,"indexed":true,"changed_files":[],"changed_symbols":[{"name":"Run"}],"blast_radius":[{"name":"Caller"}],"covering_tests":[],"untested_symbols":[],"stale":false}`,
		`{"schema_version":1,"project":"x","mode":"working","depth":3,"is_repo":true,"indexed":true,"changed_files":[],"changed_symbols":[{"symbol":"Run","kind":"function","file":"a.go","start_line":7,"end_line":5}],"blast_radius":[],"covering_tests":[],"untested_symbols":[],"stale":false}`,
		`{"schema_version":1,"project":"x","mode":"working","depth":3,"is_repo":true,"indexed":true,"changed_files":[],"changed_symbols":[],"blast_radius":[],"covering_tests":[],"untested_symbols":[],"stale":false,"risk":{"level":"high","score":2,"factors":[]}}`,
	}
	for _, fixture := range fixtures {
		res, _ := (&Codemap{tool: fakeTool(fixture, "", 0)}).Execute(context.Background(), Request{Operation: "review"})
		if res.Status != StatusPartial || len(res.Facts) != 0 {
			t.Errorf("incompatible versioned review must be partial with no facts: %s => %s %+v", fixture, res.Status, res.Facts)
		}
	}
}

func TestCodemapNotFound(t *testing.T) {
	c := &Codemap{tool: fakeTool(`{"symbol":"Nope","found":false}`, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"symbol": "Nope"}})
	if res.Status != StatusPartial {
		t.Errorf("a not-found symbol should be partial, got %s", res.Status)
	}
}

func TestCodemapBadJSONDegrades(t *testing.T) {
	c := &Codemap{tool: fakeTool("not json at all", "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"symbol": "X"}})
	if res.Status != StatusPartial {
		t.Errorf("unparseable output should degrade to partial, got %s", res.Status)
	}
}

// ---- vecgrep (bare array output) ----

func TestVecgrepSearchParse(t *testing.T) {
	fixture := `[{"file_path":"a.go","relative_path":"a.go","start_line":10,"symbol_name":"Foo","score":0.91},
	  {"relative_path":"b.go","start_line":5,"chunk_type":"block","score":0.42}]`
	v := &Vecgrep{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "auth"}})
	if res.Status != StatusAuthoritative || len(res.Facts) != 2 {
		t.Fatalf("expected 2 facts, got %d (status %s)", len(res.Facts), res.Status)
	}
	// discovery hits are candidates → low confidence.
	if res.Facts[0].Confidence != "low" {
		t.Errorf("search hits should be low confidence, got %s", res.Facts[0].Confidence)
	}
}

func TestVecgrepEmptyArray(t *testing.T) {
	v := &Vecgrep{tool: fakeTool(`[]`, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "x"}})
	if res.Status != StatusAuthoritative || len(res.Facts) != 0 {
		t.Errorf("empty result should be authoritative with 0 facts, got %d (%s)", len(res.Facts), res.Status)
	}
}

// ---- fcheap (connect emits {matches:[SearchResult]}, an object not an array) ----

func TestFcheapConnectParse(t *testing.T) {
	// Real shape (fcheap analyze.ConnectResult): an object with a matches array of
	// {stash_id, score, text, file, source}, no line number.
	fixture := `{"stash_id":"s1","codebase":"/repo","query":"q","matches":[
	  {"stash_id":"s1","file":"src/x.go","score":0.8,"text":"func Handle(){}","source":"hybrid"},
	  {"stash_id":"s1","file":"src/y.go","score":0.5,"text":"helper"}]}`
	f := &Fcheap{tool: fakeTool(fixture, "", 0)}
	res, _ := f.Execute(context.Background(), Request{Operation: "connect", Input: map[string]any{"stash": "s1", "codebase": "/repo"}})
	if res.Status != StatusAuthoritative || len(res.Facts) != 2 {
		t.Fatalf("expected 2 connect facts, got %d (%s)", len(res.Facts), res.Status)
	}
	if res.Facts[0].Location == nil || res.Facts[0].Location.File != "src/x.go" {
		t.Errorf("connect fact should carry a code location, got %+v", res.Facts[0].Location)
	}
	if !strings.Contains(res.Facts[0].Claim, "func Handle") {
		t.Errorf("connect fact should include the matched snippet text, got: %s", res.Facts[0].Claim)
	}
}

func TestFcheapSaveParsesFlatManifest(t *testing.T) {
	// Regression for the flat-manifest bug: fcheap save --json is {id,…}, not {manifest:{id}}.
	f := &Fcheap{tool: fakeTool(`{"id":"rb_123","tool":"cortex","file_count":2}`, "", 0)}
	id, err := f.Save(context.Background(), "/repo", "/bundle", []string{"t"}, "cortex")
	if err != nil || id != "rb_123" {
		t.Errorf("Save should parse the flat manifest id, got id=%q err=%v", id, err)
	}
}

// ---- behavioral (cairntrace/glyphrun) ----

func TestBehavioralExitCodeClassification(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		exit   int
		want   string // marker substring the kernel keys on
	}{
		{"pass", `{"status":"passed","runDir":"/r"}`, 0, "PASSED"},
		{"fail", `{"status":"failed"}`, 1, markFailed},
		{"errored-status", `{"status":"errored"}`, 2, markErrored},
		{"errored-exitcode", ``, 6, markErrored}, // contract-hash mismatch, no JSON
		// exit 0 but the payload is non-empty and UNPARSEABLE (schema drift, a
		// banner, output redirected): the verdict is unreadable, so it must NOT be
		// laundered into a confident PASS — treat as errored/inconclusive.
		{"unreadable-exit0", `Ran 3 checks, all good!`, 0, markErrored},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &Glyphrun{tool: fakeTool(tc.stdout, "", tc.exit)}
			res, _ := g.Execute(context.Background(), Request{Operation: "run", Input: map[string]any{"spec": "s.yml"}})
			joined := res.Summary + " | " + strings.Join(res.Warnings, " | ")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("exit %d: expected %q in %q", tc.exit, tc.want, joined)
			}
		})
	}
}

// ---- tvault (safe metadata only) ----

func TestTvaultAvailabilityParse(t *testing.T) {
	tv := &Tvault{tool: fakeTool(`[{"name":"app-staging"},{"name":"app-ci"}]`, "", 0)}
	res, _ := tv.Execute(context.Background(), Request{Operation: "availability", Input: map[string]any{"project": "app-staging"}})
	if res.Status != StatusAuthoritative {
		t.Fatalf("status = %s", res.Status)
	}
	if !strings.Contains(factClaims(res), "true") {
		t.Errorf("project app-staging should be reported available, got: %s", factClaims(res))
	}
	// A project not in the list is reported unavailable.
	res2, _ := tv.Execute(context.Background(), Request{Operation: "availability", Input: map[string]any{"project": "missing"}})
	if !strings.Contains(factClaims(res2), "false") {
		t.Errorf("missing project should be reported unavailable, got: %s", factClaims(res2))
	}
}

// ---- vidtrace (shapes captured from the real CLI) ----

func TestVidtraceInvestigateSuccessParse(t *testing.T) {
	// Real base shape: {ok, query, bundle_dir, evidence:[{time_seconds, ocr, ...}]}.
	fixture := `{"ok":true,"query":"checkout","bundle_dir":"/tmp/b",
	  "evidence":[{"score":2.07,"time_seconds":37,"frame":"frames/frame_0038.png","ocr":"Does your company have a website?","source_video":"/d/OPG.mp4"}]}`
	v := &Vidtrace{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "investigate", Input: map[string]any{"query": "checkout", "stash": "s1"}})
	if res.Status != StatusAuthoritative {
		t.Fatalf("status = %s", res.Status)
	}
	if !strings.Contains(factClaims(res), "at 37s the video shows") {
		t.Errorf("should extract the visible-failure frame, got: %s", factClaims(res))
	}
}

func TestVidtraceInvestigateErrorIsPartial(t *testing.T) {
	// Real error shape: {ok:false, error} — must NOT be reported as success.
	v := &Vidtrace{tool: fakeTool(`{"ok":false,"error":"bundle validation failed"}`, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "investigate", Input: map[string]any{"query": "x", "stash": "bad"}})
	if res.Status != StatusPartial {
		t.Errorf("an ok:false investigate must be partial, not %s", res.Status)
	}
	if len(res.Facts) != 0 {
		t.Errorf("a failed investigation must not fabricate facts, got %d", len(res.Facts))
	}
}

func TestVidtraceInvestigateConnectMatches(t *testing.T) {
	// Real --connect shape: code_matches:[{file, text, score, source}] (no line).
	fixture := `{"ok":true,"query":"q","evidence":[],"summary":"...",
	  "code_matches":[{"file":"src/checkout.ts","text":"function handleClick(){}","score":0.9,"source":"vecgrep"}]}`
	v := &Vidtrace{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "investigate", Input: map[string]any{"query": "q", "stash": "s", "connect": true}})
	var f *Fact
	for i := range res.Facts {
		if res.Facts[i].Location != nil {
			f = &res.Facts[i]
		}
	}
	if f == nil || f.Location.File != "src/checkout.ts" {
		t.Fatalf("connect code_match should become a located fact, got %+v", f)
	}
	if strings.Contains(f.Claim, ":0") {
		t.Errorf("claim should omit line when absent, got: %s", f.Claim)
	}
	if !strings.Contains(f.Claim, "handleClick") {
		t.Errorf("claim should include the code snippet text, got: %s", f.Claim)
	}
}

func TestVidtraceStashListWrappedParse(t *testing.T) {
	// Regression: stash list --json is {ok, stashes:[…]}, not a bare array.
	fixture := `{"ok":true,"stashes":[{"id":"vt_1","name":"OPG-15135","tool":"vidtrace"},{"id":"vt_2","tool":"vidtrace"}]}`
	v := &Vidtrace{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "stash_list"})
	if res.Status != StatusAuthoritative || len(res.Artifacts) != 2 || len(res.Facts) != 2 {
		t.Fatalf("expected 2 stashes as facts+artifacts, got %d facts / %d artifacts (%s)", len(res.Facts), len(res.Artifacts), res.Status)
	}
	if !strings.Contains(factClaims(res), "--video vt_1") {
		t.Errorf("stash fact should tell the model how to investigate it, got: %s", factClaims(res))
	}
}

func TestVidtraceStashListErrorIsPartial(t *testing.T) {
	// Regression: stash list reports failure in-band with {ok:false, error}. It
	// must surface as partial, never a fabricated "0 archived bundles" success.
	v := &Vidtrace{tool: fakeTool(`{"ok":false,"error":"stash index unreadable"}`, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "stash_list"})
	if res.Status != StatusPartial {
		t.Errorf("an ok:false stash_list must be partial, not %s", res.Status)
	}
	if len(res.Facts) != 0 || len(res.Artifacts) != 0 {
		t.Errorf("a failed stash_list must not fabricate stashes, got %d facts / %d artifacts", len(res.Facts), len(res.Artifacts))
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "stash index unreadable") {
		t.Errorf("the vidtrace error should be surfaced as a warning, got %v", res.Warnings)
	}
}

// ---- additional adapter parse paths ----

func TestCodemapCallersParse(t *testing.T) {
	fixture := `{"symbol":"Foo","found":true,"resolution":"precise","results":[{"symbol":"A"},{"symbol":"B"}]}`
	c := &Codemap{tool: fakeTool(fixture, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "callers", Input: map[string]any{"symbol": "Foo"}})
	if res.Status != StatusAuthoritative || len(res.Facts) != 1 {
		t.Fatalf("callers: status %s, %d facts", res.Status, len(res.Facts))
	}
	if !strings.Contains(res.Summary, "2 result") {
		t.Errorf("callers summary should count results, got %q", res.Summary)
	}
}

func TestCodemapSemanticIsLowConfidence(t *testing.T) {
	fixture := `{"query":"auth","mode":"semantic","hits":[{"symbol":"Login","kind":"func","file":"a.go","start_line":1,"score":0.8}]}`
	c := &Codemap{tool: fakeTool(fixture, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "semantic", Input: map[string]any{"query": "auth"}})
	if len(res.Facts) != 1 || res.Facts[0].Confidence != "low" {
		t.Errorf("semantic hits should be low confidence, got %+v", res.Facts)
	}
}

func TestCodemapFindIsMediumConfidence(t *testing.T) {
	fixture := `{"query":"Login","mode":"name","hits":[{"symbol":"Login","kind":"func","file":"a.go","start_line":1}]}`
	c := &Codemap{tool: fakeTool(fixture, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "find", Input: map[string]any{"query": "Login"}})
	if len(res.Facts) != 1 || res.Facts[0].Confidence != "medium" {
		t.Errorf("find (by name) should be medium confidence, got %+v", res.Facts)
	}
}

func TestFcheapSearchParse(t *testing.T) {
	// Real shape: bare array of SearchResult {stash_id, score, text, file, source}
	// — the snippet is `text`, the stash id is `stash_id` (not `snippet`/`stash`).
	fixture := `[{"stash_id":"s1","score":0.9,"text":"Internal Migrant error","file":"frames/f.png @ 12s","source":"keyword"}]`
	f := &Fcheap{tool: fakeTool(fixture, "", 0)}
	res, _ := f.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "error"}})
	if res.Status != StatusAuthoritative || len(res.Facts) != 1 {
		t.Fatalf("fcheap search: status %s, %d facts", res.Status, len(res.Facts))
	}
	if res.Facts[0].URI != "fcheap://stash/s1" {
		t.Errorf("search fact should reference the real stash_id, got %q", res.Facts[0].URI)
	}
	if !strings.Contains(res.Facts[0].Claim, "Internal Migrant error") {
		t.Errorf("search fact should include the matched text, got: %s", res.Facts[0].Claim)
	}
}

func TestFcheapListParse(t *testing.T) {
	// Real shape: fcheap emits file_count (not files) per stash.
	fixture := `[{"id":"s1","name":"bug-123","tool":"vidtrace","file_count":5},{"id":"s2","name":"trace","tool":"cairntrace","file_count":2}]`
	f := &Fcheap{tool: fakeTool(fixture, "", 0)}
	res, _ := f.Execute(context.Background(), Request{Operation: "list"})
	if res.Status != StatusAuthoritative || len(res.Artifacts) != 2 {
		t.Fatalf("fcheap list: status %s, %d artifacts", res.Status, len(res.Artifacts))
	}
	if !strings.Contains(res.Artifacts[0].Summary, "5 files") {
		t.Errorf("list artifact should reflect the real file_count, got: %s", res.Artifacts[0].Summary)
	}
}

func TestCairntraceRunParse(t *testing.T) {
	// cairntrace shares behavioralResult with glyphrun.
	c := &Cairntrace{tool: fakeTool(`{"status":"passed","runDir":"/r"}`, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "run", Input: map[string]any{"spec": "flow.yml"}})
	if res.Status != StatusAuthoritative || !strings.Contains(res.Summary, "PASSED") {
		t.Errorf("cairntrace pass: %s / %q", res.Status, res.Summary)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].URI != "/r" {
		t.Errorf("a browser run should record its run bundle, got %+v", res.Artifacts)
	}
}

func TestCairntraceStepErrorSurfaced(t *testing.T) {
	// Regression: cairn's RunResult v1 outcomes are {id,status} with NO message —
	// the failure reason lives on steps[].error. behavioralResult must surface it,
	// or a browser failure reduces to a bare "FAILED" with no explanation.
	fixture := `{"status":"failed","runDir":"/r","exitCode":1,
	  "outcomes":[{"id":"login_works","status":"failed"}],
	  "steps":[{"id":"click_submit","status":"failed","error":"timeout waiting for selector button[type=submit]"}]}`
	c := &Cairntrace{tool: fakeTool(fixture, "", 1)}
	res, _ := c.Execute(context.Background(), Request{Operation: "run", Input: map[string]any{"spec": "login.yml"}})
	if !strings.Contains(factClaims(res), "timeout waiting for selector") {
		t.Errorf("cairn step error should become a fact, got: %s", factClaims(res))
	}
}

func TestTvaultListKeysNeedsProject(t *testing.T) {
	tv := &Tvault{tool: fakeTool(`["DB_URL","API_KEY"]`, "", 0)}
	// Missing project → error, never leaks.
	res, _ := tv.Execute(context.Background(), Request{Operation: "list_keys"})
	if res.Status != StatusError {
		t.Errorf("list_keys with no project should error, got %s", res.Status)
	}
	// With a project → authoritative, and the fact is flagged sensitive.
	res2, _ := tv.Execute(context.Background(), Request{Operation: "list_keys", Input: map[string]any{"project": "app"}})
	if res2.Status != StatusAuthoritative || len(res2.Facts) == 0 || !res2.Facts[0].Sensitive {
		t.Errorf("list_keys should return a sensitive-flagged fact, got %s %+v", res2.Status, res2.Facts)
	}
}

func TestUnknownOperationIsError(t *testing.T) {
	c := &Codemap{tool: fakeTool("", "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "bogus"})
	if res.Status != StatusError {
		t.Errorf("an unknown operation should be an error, got %s", res.Status)
	}
}

// argAwareRunner models an OLD binary that rejects an unknown flag. cobra prints
// "Error: unknown flag: …" to stderr and exits non-zero, and the tool runner
// treats a non-zero exit as DATA (err=nil) — so the flag rejection surfaces as
// (empty stdout, stderr, exit 1, nil), NOT a runner error.
type argAwareRunner struct {
	rejectFlag    string
	stdoutWithout string
}

func (a argAwareRunner) run(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, int, error) {
	for _, x := range args {
		if x == a.rejectFlag {
			return nil, []byte("Error: unknown flag: " + a.rejectFlag + "\n"), 1, nil
		}
	}
	return []byte(a.stdoutWithout), nil, 0, nil
}

var _ = fmt.Sprintf

func TestVecgrepRecallProviderDownIsUnavailable(t *testing.T) {
	// vecgrep ≥2.15 signals a down embedding provider with exit 3; recall must be
	// unavailable (not a fabricated empty "0 memories").
	v := &Vecgrep{tool: fakeTool("", `{"error":"provider_unavailable"}`, 3)}
	res, _ := v.Execute(context.Background(), Request{Operation: "memory_recall", Input: map[string]any{"query": "returnTo"}})
	if res.Status != StatusUnavailable || len(res.Facts) == 0 || res.Facts[0].Kind != "tool_unavailable" {
		t.Errorf("provider-down recall should be unavailable, got %s %+v", res.Status, res.Facts)
	}
	// Healthy recall still parses authoritatively.
	v2 := &Vecgrep{tool: fakeTool(`[{"id":"20","content":"repo=demo finding=x","importance":0.5,"tags":["cortex","demo"],"score":0.43}]`, "", 0)}
	ok, _ := v2.Execute(context.Background(), Request{Operation: "memory_recall", Input: map[string]any{"query": "x"}})
	if ok.Status != StatusAuthoritative || len(ok.Facts) != 1 || !strings.Contains(ok.Facts[0].Claim, "prior memory") {
		t.Errorf("healthy recall should surface a prior-memory fact, got %s %+v", ok.Status, ok.Facts)
	}
}

func TestGlyphContractHashMismatchIsActionable(t *testing.T) {
	// glyph ≥v0.9 classifies a contract-hash mismatch (exit 6). The receipt must
	// tell the agent to re-stamp, and stay inconclusive (not a behavioral fail).
	fixture := `{"status":"errored","errorKind":"contract_hash_mismatch","exitCode":6,"runDir":"",
	  "contractHash":"sha256:aaaa","expectedHash":"sha256:bbbb",
	  "diagnostic":"contractHash mismatch: stamped sha256:bbbb, computed sha256:aaaa","outcomes":[]}`
	g := &Glyphrun{tool: fakeTool(fixture, "", 6)}
	res, _ := g.Execute(context.Background(), Request{Operation: "run", Input: map[string]any{"spec": "v.yml"}})
	claims := factClaims(res)
	if !strings.Contains(claims, "contract_hash_mismatch") {
		t.Errorf("errored diagnostic should become a fact, got: %s", claims)
	}
	// Keeping the markErrored substring is what the kernel maps to inconclusive
	// Asserting it here proves the mapping without importing kernel.
	joined := strings.Join(res.Warnings, " | ")
	if !strings.Contains(joined, "re-stamp") || !strings.Contains(joined, markErrored) {
		t.Errorf("hash-mismatch warning should say re-stamp and stay errored, got: %s", joined)
	}
}

func TestFcheapSaveRetriesWithoutIndexOnOldBinary(t *testing.T) {
	// New fcheap indexes on save; an old fcheap rejects --index at parse time, so
	// Save must retry without it and still archive.
	f := &Fcheap{tool: tool{bin: "git", redact: redact.New(), timeout: time.Second,
		run: argAwareRunner{rejectFlag: "--index", stdoutWithout: `{"id":"rb_1","tool":"cortex"}`}}}
	id, err := f.Save(context.Background(), "/repo", "/bundle", []string{"t"}, "cortex")
	if err != nil || id != "rb_1" {
		t.Errorf("Save should retry without --index and return the id, got id=%q err=%v", id, err)
	}
}

func TestFcheapConnectIndexMissing(t *testing.T) {
	// fcheap ≥0.28 returns exit 0 + index_status:"missing" for an unindexed
	// codebase — report that honestly, not "0 owning-code candidates".
	fixture := `{"stash_id":"s1","codebase":"/repo","query":"q","matches":[],"index_status":"missing"}`
	f := &Fcheap{tool: fakeTool(fixture, "", 0)}
	res, _ := f.Execute(context.Background(), Request{Operation: "connect", Input: map[string]any{"stash": "s1", "codebase": "/repo"}})
	if res.Status != StatusPartial || len(res.Facts) != 1 || !strings.Contains(factClaims(res), "not indexed") {
		t.Errorf("unindexed connect should be partial with a 'not indexed' fact, got %s: %s", res.Status, factClaims(res))
	}
}

func TestVidtraceConnectLineAnchor(t *testing.T) {
	// vidtrace 0.15 code_matches carry a real line — the fact anchors at file:line.
	fixture := `{"ok":true,"query":"checkout fails","bundle_dir":"/tmp/b","evidence":[],
	  "code_matches":[{"file":"src/checkout.ts","line":42,"text":"function handleClick(){}","score":0.9,"source":"vecgrep"}]}`
	v := &Vidtrace{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "investigate", Input: map[string]any{"query": "checkout fails", "stash": "s", "connect": true, "codebase": "/repo"}})
	var located *Fact
	for i := range res.Facts {
		if res.Facts[i].Location != nil {
			located = &res.Facts[i]
		}
	}
	if located == nil || located.Location.File != "src/checkout.ts" || located.Location.StartLine != 42 {
		t.Fatalf("connect fact should anchor at src/checkout.ts:42, got %+v", located)
	}
	if !strings.Contains(located.Claim, "src/checkout.ts:42") {
		t.Errorf("claim should show file:line, got: %s", located.Claim)
	}
}

func TestVidtraceUsageErrorIsPartial(t *testing.T) {
	// vidtrace 0.15 emits structured {ok:false,error} for usage errors under -json.
	v := &Vidtrace{tool: fakeTool(`{"ok":false,"error":"--connect requires --codebase"}`, "", 2)}
	res, _ := v.Execute(context.Background(), Request{Operation: "investigate", Input: map[string]any{"query": "x", "stash": "s", "connect": true, "codebase": "/repo"}})
	if res.Status != StatusPartial || len(res.Facts) != 0 {
		t.Errorf("a usage error should be partial with no fabricated facts, got %s %d facts", res.Status, len(res.Facts))
	}
	if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, " "), "--connect requires --codebase") {
		t.Errorf("the usage error reason should surface as a warning, got %v", res.Warnings)
	}
}

func TestCodemapErrorEnvelope(t *testing.T) {
	// codemap ≥0.36 prints {ok:false,error,code,hint} on stdout for a failure —
	// it must be UNAVAILABLE, not a confidently-wrong "no such symbol".
	fixture := `{"ok":false,"error":"open graph: file is not a database","code":"index_corrupt","hint":"back up the graph DB, then run: codemap index --reindex"}`
	c := &Codemap{tool: fakeTool(fixture, "", 4)}
	res, _ := c.Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"symbol": "X"}})
	if res.Status != StatusUnavailable {
		t.Errorf("an error envelope must be unavailable, not %s", res.Status)
	}
	if !strings.Contains(res.Summary+strings.Join(res.Warnings, " "), "index_corrupt") {
		t.Errorf("should surface the code+hint, got summary=%q warns=%v", res.Summary, res.Warnings)
	}
	// index_missing on review → unavailable with the reindex hint.
	miss := `{"ok":false,"error":"no index","code":"index_missing","hint":"run: codemap index"}`
	r2, _ := (&Codemap{tool: fakeTool(miss, "", 3)}).Execute(context.Background(), Request{Operation: "review"})
	if r2.Status != StatusUnavailable || !strings.Contains(strings.Join(r2.Warnings, " "), "codemap index") {
		t.Errorf("index_missing review should be unavailable + hint, got %s %v", r2.Status, r2.Warnings)
	}
	// A normal success (no `ok` key) is unaffected.
	ok, _ := (&Codemap{tool: fakeTool(`{"symbol":"F","found":true,"call_graph":"resolved"}`, "", 0)}).Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"symbol": "F"}})
	if ok.Status != StatusAuthoritative {
		t.Errorf("a success result must not be treated as an envelope, got %s", ok.Status)
	}
}

func TestCodemapCallGraphEnumConfidence(t *testing.T) {
	cases := map[string]string{"resolved": "high", "name": "medium", "unresolved": "low", "none": "low"}
	for cg, want := range cases {
		fixture := fmt.Sprintf(`{"symbol":"F","found":true,"call_graph":%q,"blast_radius":[{"symbol":"A"}],"tests":[{"symbol":"T"}]}`, cg)
		c := &Codemap{tool: fakeTool(fixture, "", 0)}
		res, _ := c.Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"symbol": "F"}})
		if res.Facts[0].Confidence != want {
			t.Errorf("call_graph=%q should be %s confidence, got %s", cg, want, res.Facts[0].Confidence)
		}
	}
	// unresolved surfaces the --precise hint.
	un := `{"symbol":"F","found":true,"call_graph":"unresolved","resolution":"call graph not available for typescript without precise indexing — run 'codemap index --precise' to resolve them","blast_radius":[],"tests":[]}`
	res, _ := (&Codemap{tool: fakeTool(un, "", 0)}).Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"symbol": "F"}})
	if !strings.Contains(strings.Join(res.Warnings, " "), "--precise") {
		t.Errorf("unresolved should warn with the --precise hint, got %v", res.Warnings)
	}
}

func TestCodemapReviewRiskBand(t *testing.T) {
	fixture := `{"indexed":true,"is_repo":true,"changed_files":[{"path":"a.go","status":"M","symbols":1}],
	  "changed_symbols":[{"symbol":"Hub"}],"blast_radius":[{"symbol":"C0"}],"covering_tests":[],
	  "untested_symbols":[{"symbol":"Hub"}],"hotspots":[{"symbol":"Hub"}],"call_graph":"name",
	  "risk":{"level":"high","score":0.95,"factors":[
	    {"factor":"untested_changes","severity":0.9,"detail":"no covering test"},
	    {"factor":"hotspot_fanin","severity":0.5,"detail":"hub with many callers"}]}}`
	c := &Codemap{tool: fakeTool(fixture, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "review"})
	joined := factClaims(res) + strings.Join(res.Warnings, " ")
	if !strings.Contains(joined, "diff risk: high") || !strings.Contains(joined, "untested_changes") || !strings.Contains(joined, "hotspot_fanin") {
		t.Errorf("review should surface the risk band + factors, got: %s", joined)
	}
	// A review with no risk key still parses (old codemap).
	noRisk := `{"indexed":true,"is_repo":true,"changed_files":[{"path":"a.go","status":"M"}],"changed_symbols":[],"covering_tests":[]}`
	r2, _ := (&Codemap{tool: fakeTool(noRisk, "", 0)}).Execute(context.Background(), Request{Operation: "review"})
	if r2.Status != StatusAuthoritative || strings.Contains(strings.Join(r2.Warnings, " "), "diff risk") {
		t.Errorf("a review with no risk band must not emit a risk warning, got %s %v", r2.Status, r2.Warnings)
	}
	explicitZero := strings.Replace(noRisk, "{", `{"schema_version":0,`, 1)
	r3, _ := (&Codemap{tool: fakeTool(explicitZero, "", 0)}).Execute(context.Background(), Request{Operation: "review"})
	if r3.Status != StatusPartial || len(r3.Facts) != 0 {
		t.Errorf("explicit schema version 0 must be rejected; only an absent version is legacy, got %s %+v", r3.Status, r3.Facts)
	}
}

// TestCodemapReviewExportedSymbolEscalation guards the public-
// contract escalation: an indexed review with an exported changed symbol warns
// that the diff touches a public-contract surface.
func TestCodemapReviewExportedSymbolEscalation(t *testing.T) {
	exported := `{"indexed":true,"is_repo":true,"changed_files":[{"path":"a.go","status":"M","symbols":2}],
	  "changed_symbols":[{"symbol":"HandleCallback"},{"symbol":"helper"}],"covering_tests":[],"call_graph":"resolved"}`
	res, _ := (&Codemap{tool: fakeTool(exported, "", 0)}).Execute(context.Background(), Request{Operation: "review"})
	joined := strings.Join(res.Warnings, " ")
	if !strings.Contains(joined, "exported symbol") || !strings.Contains(joined, "public-contract change") {
		t.Errorf("an exported changed symbol should trigger the public-contract escalation warning, got: %s", joined)
	}
	// Only-private symbols (leading lowercase/underscore) do NOT trigger it.
	priv := `{"indexed":true,"is_repo":true,"changed_files":[{"path":"a.go","status":"M","symbols":1}],
	  "changed_symbols":[{"symbol":"helper"},{"symbol":"_internal"}],"covering_tests":[],"call_graph":"resolved"}`
	r2, _ := (&Codemap{tool: fakeTool(priv, "", 0)}).Execute(context.Background(), Request{Operation: "review"})
	if strings.Contains(strings.Join(r2.Warnings, " "), "exported symbol") {
		t.Errorf("non-exported symbols must not trigger the public-contract escalation, got: %v", r2.Warnings)
	}
	// An unindexed review has no changed_symbols, so no escalation.
	unindexed := `{"indexed":false,"is_repo":true,"changed_files":[{"path":"a.go","status":"M"}],"changed_symbols":[],"note":"not indexed"}`
	r3, _ := (&Codemap{tool: fakeTool(unindexed, "", 0)}).Execute(context.Background(), Request{Operation: "review"})
	if strings.Contains(strings.Join(r3.Warnings, " "), "exported symbol") {
		t.Errorf("unindexed review must not emit an exported-symbol warning, got: %v", r3.Warnings)
	}
}

func TestTvaultVaultLockedIsUnavailable(t *testing.T) {
	// tvault ≥0.16 signals a locked vault deterministically (exit 3); it must be
	// an honest "unavailable", not an opaque degrade.
	tv := &Tvault{tool: fakeTool(`{"error":"vault_locked","locked":true}`, "", 3)}
	res, _ := tv.Execute(context.Background(), Request{Operation: "availability", Input: map[string]any{"project": "app"}})
	if res.Status != StatusUnavailable {
		t.Errorf("a locked vault should be unavailable, got %s", res.Status)
	}
}

func TestTvaultNamesOnlyFallsBackOnOldBinary(t *testing.T) {
	// An old tvault rejects --names-only; the adapter retries the plain form and
	// still answers from the shape-identical output.
	tv := &Tvault{tool: tool{bin: "git", redact: redact.New(), timeout: time.Second,
		run: argAwareRunner{rejectFlag: "--names-only", stdoutWithout: `[{"name":"app"},{"name":"api"}]`}}}
	res, _ := tv.Execute(context.Background(), Request{Operation: "availability", Input: map[string]any{"project": "api"}})
	if res.Status != StatusAuthoritative || !strings.Contains(res.Summary, "true") {
		t.Errorf("names-only should fall back to the plain form on an old binary, got %s %q", res.Status, res.Summary)
	}
}

func TestVecgrepEnvelopeIndexedWithSnippet(t *testing.T) {
	// json-envelope with an index and hits → authoritative, and the matched
	// content snippet enriches the fact.
	fixture := `{"schema_version":1,"index":{"indexed":true,"fresh":false,"chunks":2126},"hits":[
	  {"chunk_id":305,"relative_path":"internal/embed/provider.go","start_line":23,"end_line":45,"symbol_name":"Provider","language":"go","content":"func (p *Provider) Embed() error {","score":0.62}]}`
	v := &Vecgrep{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "embed"}})
	if res.Status != StatusAuthoritative || len(res.Facts) != 1 {
		t.Fatalf("indexed envelope should be authoritative with 1 fact, got %s / %d", res.Status, len(res.Facts))
	}
	if !strings.Contains(res.Facts[0].Claim, "func (p *Provider) Embed") {
		t.Errorf("search fact should include the matched snippet, got: %s", res.Facts[0].Claim)
	}
}

func TestVecgrepEnvelopeUnknownSchemaDegrades(t *testing.T) {
	fixture := `{"schema_version":2,"index":{"indexed":true,"fresh":true,"chunks":1},"hits":[]}`
	v := &Vecgrep{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "x"}})
	if res.Status != StatusPartial {
		t.Fatalf("unknown vecgrep schema should degrade, got %s", res.Status)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "schema version 2") {
		t.Fatalf("missing schema warning: %v", res.Warnings)
	}
}

func TestVecgrepEnvelopeNoIndexIsUnavailable(t *testing.T) {
	// An absent index must be an honest "unavailable", never a false empty result.
	fixture := `{"index":{"indexed":false,"fresh":false,"chunks":0},"hits":[]}`
	v := &Vecgrep{tool: fakeTool(fixture, "", 0)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "x"}})
	if res.Status != StatusUnavailable || len(res.Facts) == 0 || res.Facts[0].Kind != "tool_unavailable" {
		t.Errorf("an unindexed workspace should be unavailable, got %s %+v", res.Status, res.Facts)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "vecgrep index") {
		t.Errorf("should hint to build the index, got %v", res.Warnings)
	}
}

func TestVecgrepNotAProjectIsUnavailable(t *testing.T) {
	// The text-error form of "no index" maps to the same signal (not degraded).
	v := &Vecgrep{tool: fakeTool("", "Error: not in a vecgrep project: run 'vecgrep init' first", 1)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "x"}})
	if res.Status != StatusUnavailable {
		t.Errorf("a 'not a vecgrep project' error should be unavailable, got %s", res.Status)
	}
}

func TestVecgrepBrokenIndexIsNotAuthoritative(t *testing.T) {
	// Dogfooding 2026-07-07 root cause: with a broken embedding profile, vecgrep
	// exits non-zero but can still emit an indexed-but-empty envelope. That must
	// NOT be reported as an authoritative empty result (a confident false
	// all-clear) — the non-zero exit downgrades it to a degraded/partial signal.
	fixture := `{"index":{"indexed":true,"fresh":false,"chunks":2126},"hits":[]}`
	stderr := "Error: search failed: embedding profile is missing for an existing index; run 'vecgrep index --full'"
	v := &Vecgrep{tool: fakeTool(fixture, stderr, 1)}
	res, _ := v.Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "supplier signup address"}})
	if res.Status == StatusAuthoritative {
		t.Errorf("a non-zero-exit search must not be authoritative, got %s", res.Status)
	}
	if res.Status != StatusPartial {
		t.Errorf("a broken-index search should degrade to partial, got %s", res.Status)
	}
}

func TestVecgrepSimilarNonZeroExitIsNotAuthoritative(t *testing.T) {
	// The same guard applies to `similar`: a parseable array from a non-zero exit
	// is still a failed neighbor search, never authoritative.
	v := &Vecgrep{tool: fakeTool(`[]`, "Error: search failed", 1)}
	res, _ := v.Execute(context.Background(), Request{Operation: "similar", Input: map[string]any{"target": "internal/x.go"}})
	if res.Status == StatusAuthoritative {
		t.Errorf("a non-zero-exit similar must not be authoritative, got %s", res.Status)
	}
}

func TestCairntraceCanonicalFailure(t *testing.T) {
	// cairn ≥1.30 RunResult (real live shape): summary + a canonical failure
	// object + always-present spec.contractHash. The receipt must carry the
	// authoritative reason, not a bare "FAILED".
	fixture := `{"status":"failed","runDir":"/r","exitCode":1,
	  "spec":{"name":"row-count","path":"/x.yml","contractHash":"sha256:38baed86961854048e916aa512aed3ebe38d2a3952e01e4"},
	  "summary":"outcome 'three_inventory_rows' failed",
	  "failure":{"outcome":"three_inventory_rows","message":"outcome 'three_inventory_rows' failed: expected exactly 3 element(s) matching \"[role=row], tr\"; actual observed 0 element(s)"},
	  "outcomes":[{"id":"three_inventory_rows","status":"failed"}],"steps":[]}`
	c := &Cairntrace{tool: fakeTool(fixture, "", 1)}
	res, _ := c.Execute(context.Background(), Request{Operation: "run", Input: map[string]any{"spec": "row-count.yml"}})
	claims := factClaims(res)
	if !strings.Contains(claims, "expected exactly 3 element(s)") {
		t.Errorf("the canonical failure message should become a fact, got: %s", claims)
	}
	joined := strings.Join(res.Warnings, " | ")
	if !strings.Contains(joined, markFailed) || !strings.Contains(joined, "expected exactly 3") {
		t.Errorf("the failed warning should carry the canonical reason, got: %s", joined)
	}
	// The run bundle records the contract identity.
	if len(res.Artifacts) != 1 || !strings.Contains(res.Artifacts[0].Summary, "contract sha256:") {
		t.Errorf("the run bundle should record the contract hash, got %+v", res.Artifacts)
	}
	// No duplicate per-step fact when the canonical failure is present.
	if strings.Count(claims, "failed") > 2 {
		t.Errorf("canonical failure should supersede per-step scan (no duplicates), got: %s", claims)
	}
}

func TestCairntraceSelectSpecs(t *testing.T) {
	// cairn run --select-only --json → {selected:[{name,path}]} (real 1.30 shape).
	// The runner returns the selection ONLY when the required spec positional (".")
	// is present — a regression guard, since `cairn run --select-only` without a
	// spec errors "missing required argument 'spec'".
	fixture := `{"$schema":"urn:cairntrace.dev:selection:v1","version":"1","codemapAvailable":true,
	  "selected":[{"name":"checkout","path":"/repo/specs/checkout.yml"},{"name":"login","path":"/repo/specs/login.yml"}],
	  "skipped":[{"name":"admin","reason":"no coverage"}]}`
	c := &Cairntrace{tool: tool{bin: "git", redact: redact.New(), timeout: time.Second,
		run: needArgRunner{need: ".", okOut: fixture, errOut: "error: missing required argument 'spec'"}}}
	paths, err := c.SelectSpecs(context.Background(), "/repo", "HEAD~1")
	if err != nil || len(paths) != 2 || paths[0] != "/repo/specs/checkout.yml" {
		t.Fatalf("SelectSpecs must pass the spec positional and return the selected paths, got %v err=%v", paths, err)
	}
}

// needArgRunner returns okOut only when a required arg is present, else an error
// on stderr with a non-zero exit — modelling a CLI with a required positional.
type needArgRunner struct {
	need, okOut, errOut string
}

func (a needArgRunner) run(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, int, error) {
	for _, x := range args {
		if x == a.need {
			return []byte(a.okOut), nil, 0, nil
		}
	}
	return nil, []byte(a.errOut), 1, nil
}

func TestGlyphAffectedSpecs(t *testing.T) {
	// glyph affected-specs --format json → {specs:[{name,path,coversSymbol}]}.
	fixture := `{"schemaVersion":1,"mode":"since","total":3,"matched":1,"unmatched":2,"noCover":0,
	  "specs":[{"name":"cli-help","path":"/repo/specs/help.yml","coversSymbol":"HelpCmd","matchedBy":"changed"}]}`
	g := &Glyphrun{tool: fakeTool(fixture, "", 0)}
	paths, err := g.AffectedSpecs(context.Background(), "/repo", "HEAD~1")
	if err != nil || len(paths) != 1 || paths[0] != "/repo/specs/help.yml" {
		t.Fatalf("AffectedSpecs should return the matched spec paths, got %v err=%v", paths, err)
	}
	// An empty selection (nothing covers the change) is not an error.
	g2 := &Glyphrun{tool: fakeTool(`{"schemaVersion":1,"total":3,"matched":0,"specs":[]}`, "", 0)}
	if paths, err := g2.AffectedSpecs(context.Background(), "/repo", ""); err != nil || len(paths) != 0 {
		t.Errorf("empty affected-specs should be (nil, nil), got %v err=%v", paths, err)
	}
}

func TestTvaultVaultLockedNoFalsePositiveOnSuccess(t *testing.T) {
	// Regression: a SUCCESSFUL listing (exit 0) whose names legitimately contain
	// "vault_locked" must NOT be misread as a locked vault.
	tv := &Tvault{tool: fakeTool(`[{"name":"vault_locked_demo"},{"name":"api"}]`, "", 0)}
	res, _ := tv.Execute(context.Background(), Request{Operation: "availability", Input: map[string]any{"project": "api"}})
	if res.Status != StatusAuthoritative {
		t.Errorf("a successful listing must not be misread as locked, got %s", res.Status)
	}
	// A real locked signal (exit 3) is still unavailable.
	locked := &Tvault{tool: fakeTool(`{"error":"vault_locked"}`, "", 3)}
	r2, _ := locked.Execute(context.Background(), Request{Operation: "availability", Input: map[string]any{"project": "api"}})
	if r2.Status != StatusUnavailable {
		t.Errorf("exit-3 locked signal should still be unavailable, got %s", r2.Status)
	}
}
