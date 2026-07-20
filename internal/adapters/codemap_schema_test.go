package adapters

import (
	"context"
	"strings"
	"testing"
)

func TestRequireFields(t *testing.T) {
	if err := requireFields(`{"found":false,"symbol":"x"}`, "found"); err != nil {
		t.Errorf("a present field should pass: %v", err)
	}
	if err := requireFields(`{"symbol":"x"}`, "found"); err == nil {
		t.Error("a missing required field should error")
	}
	if err := requireFields(`{"found":null}`, "found"); err == nil {
		t.Error("a null required field should error")
	}
	err := requireFields(`{"a":1,"b":2}`, "a", "c")
	if err == nil || !strings.Contains(err.Error(), "c") {
		t.Errorf("the error should name the missing field: %v", err)
	}
}

func TestCodemapImpactDegradesOnRenamedFoundField(t *testing.T) {
	// A schema rename that drops "found" must degrade loudly — not read Found=false
	// and report a confidently-wrong "no symbol".
	c := &Codemap{tool: fakeTool(`{"symbol":"HandleCallback","locations":[]}`, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"symbol": "HandleCallback"}})
	if res.Status != StatusPartial {
		t.Fatalf("status = %s, want partial (schema drift)", res.Status)
	}
	if !strings.Contains(res.Summary, "unexpected output shape") {
		t.Errorf("summary should flag the schema drift, got: %s", res.Summary)
	}
	if strings.Contains(res.Summary, "found no symbol") {
		t.Errorf("must NOT report a confident 'no symbol' on schema drift: %s", res.Summary)
	}
}

func TestCodemapImpactFoundFalseIsStillNoSymbol(t *testing.T) {
	// A present found=false is a legitimate "no symbol", not schema drift.
	c := &Codemap{tool: fakeTool(`{"symbol":"Nope","found":false}`, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "impact", Input: map[string]any{"symbol": "Nope"}})
	if res.Status != StatusPartial || !strings.Contains(res.Summary, "found no symbol") {
		t.Fatalf("found=false should be a clean 'no symbol', got status=%s summary=%s", res.Status, res.Summary)
	}
}

func TestCodemapFindDegradesOnRenamedHitsField(t *testing.T) {
	c := &Codemap{tool: fakeTool(`{"query":"callback"}`, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "find", Input: map[string]any{"query": "callback"}})
	if res.Status != StatusPartial || !strings.Contains(res.Summary, "unexpected output shape") {
		t.Fatalf("missing hits should degrade as schema drift, got status=%s summary=%s", res.Status, res.Summary)
	}
}

func TestCodemapCallersDegradesOnRenamedFoundField(t *testing.T) {
	c := &Codemap{tool: fakeTool(`{"symbol":"Foo"}`, "", 0)}
	res, _ := c.Execute(context.Background(), Request{Operation: "callers", Input: map[string]any{"symbol": "Foo"}})
	if res.Status != StatusPartial || !strings.Contains(res.Summary, "unexpected output shape") {
		t.Fatalf("missing found should degrade as schema drift, got status=%s summary=%s", res.Status, res.Summary)
	}
}
