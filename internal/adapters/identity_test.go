package adapters

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCodemapBatchPreservesIdentityFreshnessAndOrigins(t *testing.T) {
	for _, freshness := range []string{`{"checked":true,"stale":false}`, `{"checked":true,"stale":true}`, `{"checked":false,"stale":false}`} {
		t.Run(freshness, func(t *testing.T) {
			targets := []Request{
				{Input: map[string]any{"symbol": "Run", "file": "a.go", "start_line": 2, "fqn": "p.A.Run", "kind": "method"}},
				{Input: map[string]any{"symbol": "Run", "file": "b.go", "start_line": 2, "fqn": "p.B.Run", "kind": "method"}},
			}
			runner := &countingArgsRunner{onCall: func(args []string) (string, string, int) {
				var selectors []cmSelector
				for i, arg := range args {
					if arg == "--selector" {
						var sel cmSelector
						if err := json.Unmarshal([]byte(args[i+1]), &sel); err != nil {
							t.Fatal(err)
						}
						selectors = append(selectors, sel)
					}
				}
				if len(selectors) != 2 || selectors[0].FQN != "p.A.Run" || selectors[1].File != "b.go" {
					t.Fatalf("selectors = %+v", selectors)
				}
				return `{"indexed":true,"freshness":` + freshness + `,"results":[
     {"found":true,"symbol":"Run","call_graph":"resolved","selector":{"file":"a.go","start_line":12,"fqn":"p.A.Run","kind":"method"},"blast_radius":[],"tests":[]},
     {"found":true,"symbol":"Run","call_graph":"resolved","selector":{"file":"b.go","start_line":22,"fqn":"p.B.Run","kind":"method"},"blast_radius":[],"tests":[]}]}`, "", 0
			}}
			tool := fakeTool("", "", 0)
			tool.run = runner
			got, err := (&Codemap{tool: tool}).Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"targets": targets}})
			if err != nil || len(got.Facts) != 2 || runner.n != 1 {
				t.Fatalf("batch=%+v calls=%d err=%v", got, runner.n, err)
			}
			for i, f := range got.Facts {
				if f.InputIndex == nil || *f.InputIndex != i {
					t.Fatalf("origin=%+v", f)
				}
			}
			want := StatusPartial
			if strings.Contains(freshness, `"checked":true,"stale":false`) {
				want = StatusAuthoritative
			}
			if got.Status != want {
				t.Fatalf("status=%s want=%s", got.Status, want)
			}
		})
	}
}

func TestCodemapReadinessDoesNotInventFreshness(t *testing.T) {
	for _, raw := range []string{"", `,"stale":null`, `,"stale":{}`, `,"stale":{"changed":0}`, `,"stale":-1`} {
		result, _ := (&Codemap{tool: fakeTool(`{"project":"p","registered":true,"nodes":2,"files":1`+raw+`}`, "", 0)}).Execute(context.Background(), Request{Operation: "status"})
		if result.Status != StatusAuthoritative || result.Facts[0].Attributes["freshness"] != "unchecked" || result.Facts[0].Confidence == "high" {
			t.Fatalf("raw=%s result=%+v", raw, result)
		}
	}
}

func TestVecgrepSelectorAndStaleSearchDoNotRetry(t *testing.T) {
	runner := &countingArgsRunner{onCall: func([]string) (string, string, int) {
		return `{"schema_version":1,"index":{"indexed":true,"fresh":false,"chunks":1},"hits":[{"relative_path":"a.go","start_line":20,"end_line":30,"symbol_name":"Run","content":"func Run() error { return doWork() }","score":0.8,"selector":{"file":"a.go","start_line":10,"fqn":"p.Run","kind":"function"}}]}`, "", 0
	}}
	tool := fakeTool("", "", 0)
	tool.run = runner
	result, _ := (&Vecgrep{tool: tool}).Execute(context.Background(), Request{Operation: "search", Input: map[string]any{"query": "work"}})
	if runner.n != 1 || result.Status != StatusPartial || result.Freshness != "stale" || len(result.Facts) != 1 {
		t.Fatalf("result=%+v calls=%d", result, runner.n)
	}
	if got := result.Facts[0].Location; !reflect.DeepEqual(got, &Location{File: "a.go", StartLine: 10, EndLine: 30, Symbol: "Run", FQN: "p.Run", Kind: "function"}) {
		t.Fatalf("location=%+v", got)
	}
}

type deadlineRunner struct{ deadlines []time.Time }

func (r *deadlineRunner) run(ctx context.Context, _, _ string, _ ...string) ([]byte, []byte, int, error) {
	d, _ := ctx.Deadline()
	r.deadlines = append(r.deadlines, d)
	return nil, nil, -1, context.DeadlineExceeded
}
func TestExecRetriesShareOneDeadline(t *testing.T) {
	runner := &deadlineRunner{}
	tool := fakeTool("", "", 0)
	tool.run = runner
	tool.retries = 2
	_, _, _, _ = tool.exec(context.Background(), "", "query")
	if len(runner.deadlines) != 3 {
		t.Fatalf("attempts=%d", len(runner.deadlines))
	}
	for _, d := range runner.deadlines {
		if !d.Equal(runner.deadlines[0]) {
			t.Fatalf("retry renewed the deadline")
		}
	}
}
