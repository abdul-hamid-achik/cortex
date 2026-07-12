package domain

import "testing"

func TestClassifyOp(t *testing.T) {
	cases := []struct {
		tool, op string
		want     ActionClass
	}{
		{"codemap", "impact", ActionReadOnly},
		{"vecgrep", "search", ActionReadOnly},
		{"cairntrace", "run", ActionReadOnly},
		{"fcheap", "save", ActionLocalMutation},
		{"vecgrep", "remember", ActionLocalMutation},
		{"codemap", "annotate", ActionLocalMutation},
		{"tvault", "run", ActionSecretedExecution},
		{"command", "unit", ActionConfiguredExecution},
		{"anytool", "deploy", ActionExternalMutation},
		{"anytool", "publish", ActionExternalMutation},
		{"anytool", "push", ActionExternalMutation},
		{"unknown", "whatever", ActionReadOnly},
	}
	for _, tc := range cases {
		if got := ClassifyOp(tc.tool, tc.op); got != tc.want {
			t.Errorf("ClassifyOp(%q,%q) = %q, want %q", tc.tool, tc.op, got, tc.want)
		}
	}
}

func TestActionClassMutating(t *testing.T) {
	if ActionReadOnly.Mutating() {
		t.Error("read-only is not mutating")
	}
	for _, c := range []ActionClass{ActionLocalMutation, ActionExternalMutation, ActionSecretedExecution, ActionConfiguredExecution} {
		if !c.Mutating() {
			t.Errorf("%s should be mutating", c)
		}
	}
}
