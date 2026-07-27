package domain

// ActionClass classifies an operation by its side-effect risk. The
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
	// ActionExternalMutation writes outside Cortex-owned state: repository apply,
	// send, deploy, publish, push. Requires explicit approval.
	ActionExternalMutation ActionClass = "external_mutation"
	// ActionSecretedExecution runs with injected secrets (authenticated
	// integration). Requires a secrets capability and redaction.
	ActionSecretedExecution ActionClass = "secreted_execution"
	// ActionConfiguredExecution runs repository-configured argv. Even a command
	// described as a test or lint check is arbitrary local code and may access
	// the network or mutate files, so it requires an explicit trusted-harness
	// approval rather than inheriting read-only verifier status.
	ActionConfiguredExecution ActionClass = "configured_execution"
)

// Mutating reports whether the class changes state (anything but read-only).
func (c ActionClass) Mutating() bool { return c != ActionReadOnly }

// ClassifyOp maps a downstream tool operation to its action class. Only known
// query operations are read-only. Unknown tools or operations fail closed as
// external mutations until their behavior is reviewed and classified.
func ClassifyOp(tool, op string) ActionClass {
	// Outward-facing verbs are external mutation regardless of the tool that
	// issues them, so a future adapter can't smuggle a remote write past the gate.
	switch op {
	case "deploy", "publish", "push", "send", "remote_write":
		return ActionExternalMutation
	}
	switch tool {
	case "git":
		if oneOf(op, "", "status", "changed_files", "grep") {
			return ActionReadOnly
		}
	case "bob":
		// The optional Bob adapter implements only read-only context/path
		// operations. Fail closed for every other present or future verb so a
		// new Bob mutation cannot inherit the query layer's read-only default.
		if op == "context" || op == "path" {
			return ActionReadOnly
		}
		return ActionExternalMutation
	case "command":
		return ActionConfiguredExecution
	case "fcheap":
		if op == "save" {
			return ActionLocalMutation
		}
		if oneOf(op, "search", "connect", "list", "verify") {
			return ActionReadOnly
		}
	case "vecgrep":
		if op == "remember" {
			return ActionLocalMutation
		}
		if oneOf(op, "search", "similar", "memory_recall") {
			return ActionReadOnly
		}
	case "codemap":
		if op == "annotate" {
			return ActionLocalMutation
		}
		if oneOf(op, "impact", "callers", "callees", "find", "semantic", "review") {
			return ActionReadOnly
		}
	case "cairntrace", "glyphrun":
		if op == "run" {
			return ActionReadOnly
		}
	case "vidtrace":
		if oneOf(op, "investigate", "stash_list") {
			return ActionReadOnly
		}
	case "tvault":
		if op == "run" || op == "exec" {
			return ActionSecretedExecution
		}
		if oneOf(op, "", "availability", "list_keys") {
			return ActionReadOnly
		}
	case "veclite":
		if op == "case_recall" {
			return ActionReadOnly
		}
	}
	return ActionExternalMutation
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
