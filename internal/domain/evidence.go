package domain

import "time"

// EvidenceKind classifies a piece of evidence by how it was obtained (SPEC §9.2).
type EvidenceKind string

const (
	KindCodeLocation    EvidenceKind = "code_location"
	KindCodeGraph       EvidenceKind = "code_graph"
	KindSemanticSearch  EvidenceKind = "semantic_search"
	KindBrowserRun      EvidenceKind = "browser_run"
	KindTerminalRun     EvidenceKind = "terminal_run"
	KindUnitTest        EvidenceKind = "unit_test"
	KindBuild           EvidenceKind = "build"
	KindLint            EvidenceKind = "lint"
	KindArtifact        EvidenceKind = "artifact"
	KindHumanReport     EvidenceKind = "human_report"
	KindModelInference  EvidenceKind = "model_inference"
	KindToolUnavailable EvidenceKind = "tool_unavailable"
)

// verifiableKinds are evidence classes that can satisfy a verification
// requirement on their own. model_inference and human_report cannot (SPEC §9.2).
var verifiableKinds = map[EvidenceKind]bool{
	KindCodeLocation: true, KindCodeGraph: true, KindBrowserRun: true,
	KindTerminalRun: true, KindUnitTest: true, KindBuild: true,
	KindLint: true, KindArtifact: true,
}

// CanVerify reports whether this kind of evidence can, by itself, back a
// verification pass. Semantic search is a candidate, not proof.
func (k EvidenceKind) CanVerify() bool { return verifiableKinds[k] }

// Confidence is a policy band, not a probability (SPEC §8.6).
type Confidence string

const (
	ConfidenceHigh    Confidence = "high"
	ConfidenceMedium  Confidence = "medium"
	ConfidenceLow     Confidence = "low"
	ConfidenceUnknown Confidence = "unknown"
)

// Sensitivity marks whether a record may contain material that must be handled
// carefully. Cortex never writes secret values, but this flags provenance.
type Sensitivity string

const (
	SensitivityNormal    Sensitivity = "normal"
	SensitivitySensitive Sensitivity = "sensitive"
)

// Source locates where a piece of evidence came from.
type Source struct {
	Tool   string `json:"tool"`             // e.g. codemap, vecgrep, human
	RunID  string `json:"runId,omitempty"`  // adapter run identifier
	URI    string `json:"uri,omitempty"`    // tool-specific locator
	Origin string `json:"origin,omitempty"` // "human" for user-provided facts
	Actor  string `json:"actor,omitempty"`  // named human/agent that supplied it
}

// Location pins a claim to a source-code position when available.
type Location struct {
	File      string `json:"file,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
}

// Evidence is a structured claim backed by a locatable source (SPEC §8.3). A
// model statement without a source is an assertion, not evidence.
type Evidence struct {
	ID          string       `json:"id"`
	Timestamp   time.Time    `json:"timestamp"`
	Kind        EvidenceKind `json:"kind"`
	Source      Source       `json:"source"`
	Claim       string       `json:"claim"`
	Category    string       `json:"category,omitempty"` // observation | decision | constraint | handoff
	Location    *Location    `json:"location,omitempty"`
	Confidence  Confidence   `json:"confidence"`
	Sensitivity Sensitivity  `json:"sensitivity,omitempty"`
	RawRef      string       `json:"rawRef,omitempty"` // case://.../evidence/<id>
	// DerivedFrom links structurally-expanded evidence back to the discovery
	// evidence record(s) whose candidate was fed into the structural tool
	// (causal routing: symptom → candidate → structural expansion).
	DerivedFrom []string `json:"derivedFrom,omitempty"`
}

// Validate enforces the evidence invariants (SPEC §9.1): a claim, an origin,
// and a timestamp are mandatory.
func (e Evidence) Validate() error {
	if e.Claim == "" {
		return errValidation("evidence has no claim")
	}
	if e.Source.Tool == "" && e.Source.Origin == "" {
		return errValidation("evidence has no source tool or origin")
	}
	if e.Timestamp.IsZero() {
		return errValidation("evidence has no timestamp")
	}
	return nil
}

// validationError is a typed error for schema-invariant failures.
type validationError struct{ msg string }

func (e validationError) Error() string { return e.msg }

func errValidation(msg string) error { return validationError{msg: msg} }
