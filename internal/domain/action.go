package domain

// ActionClass classifies an operation by its side-effect risk (SPEC §16.3). The
// class drives the approval policy: read-only and local-mutation run freely
// within an active task, while external mutation and secret-backed execution
// require an explicit decision.
type ActionClass string

const (
	// ActionReadOnly observes without changing state: search, inspect, status,
	// graph queries, behavioral verification runs.
	ActionReadOnly ActionClass = "read_only"
	// ActionLocalMutation writes to a local store Cortex owns: a durable memory,
	// an fcheap stash, a codemap annotation.
	ActionLocalMutation ActionClass = "local_mutation"
	// ActionExternalMutation writes to a remote/outside system: send, deploy,
	// publish, push. Requires explicit approval (SPEC §16.2 #4).
	ActionExternalMutation ActionClass = "external_mutation"
	// ActionSecretedExecution runs with injected secrets (authenticated
	// integration). Requires a secrets capability and redaction (SPEC §16.3).
	ActionSecretedExecution ActionClass = "secreted_execution"
)

// Mutating reports whether the class changes state (anything but read-only).
func (c ActionClass) Mutating() bool { return c != ActionReadOnly }

// ClassifyOp maps a downstream tool operation to its action class (SPEC §16.3).
// Unknown operations default to read-only — the safe assumption for a query
// layer, since Cortex's only writes are the explicitly-classed local ones.
func ClassifyOp(tool, op string) ActionClass {
	// Outward-facing verbs are external mutation regardless of the tool that
	// issues them, so a future adapter can't smuggle a remote write past the gate.
	switch op {
	case "deploy", "publish", "push", "send", "remote_write":
		return ActionExternalMutation
	}
	switch tool {
	case "fcheap":
		if op == "save" {
			return ActionLocalMutation
		}
	case "vecgrep":
		if op == "remember" {
			return ActionLocalMutation
		}
	case "codemap":
		if op == "annotate" {
			return ActionLocalMutation
		}
	case "tvault":
		if op == "run" || op == "exec" {
			return ActionSecretedExecution
		}
	}
	return ActionReadOnly
}
