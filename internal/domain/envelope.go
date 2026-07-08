package domain

// Envelope is the shared outer result schema every Cortex tool returns
// (SPEC §10.3). Keeping one shape across start/investigate/plan/verify/status
// lets a weaker model learn the interface once.
type Envelope struct {
	OK           bool          `json:"ok"`
	TaskID       string        `json:"taskId,omitempty"`
	Phase        Phase         `json:"phase,omitempty"`
	Summary      string        `json:"summary"`
	Facts        []FactView    `json:"facts,omitempty"`
	Hypotheses   []HypView     `json:"hypotheses,omitempty"`
	Warnings     []string      `json:"warnings,omitempty"`
	NextActions  []string      `json:"nextActions,omitempty"`
	Artifacts    []ArtifactRef `json:"artifacts,omitempty"`
	RawAvailable bool          `json:"rawAvailable"`
	// Degraded is true when OK=true but one or more underlying tools this call
	// relied on returned a non-authoritative result (partial/unavailable/error) —
	// e.g. a broken vecgrep index. The facts/summary may still be present but
	// should be treated as lower-confidence than usual, not as a clean result
	// (SPEC §11.4; dogfooding 2026-07-07 found this silently buried in warnings).
	Degraded bool `json:"degraded,omitempty"`
	// Error is set (and OK=false) when a tool call fails in a recoverable way.
	Error string `json:"error,omitempty"`
}

// FactView is the compact, model-facing projection of an Evidence record. Raw
// output stays out of the envelope to protect the context window (SPEC §10.4).
type FactView struct {
	ID         string       `json:"id"`
	Claim      string       `json:"claim"`
	Confidence Confidence   `json:"confidence"`
	Source     string       `json:"source"`
	Kind       EvidenceKind `json:"kind,omitempty"`
}

// HypView is the compact projection of a Hypothesis.
type HypView struct {
	ID         string           `json:"id"`
	Statement  string           `json:"statement"`
	Confidence Confidence       `json:"confidence"`
	Status     HypothesisStatus `json:"status"`
}

// ArtifactRef is a stable pointer to a durable artifact (e.g. an fcheap stash).
type ArtifactRef struct {
	ID      string `json:"id"`
	Kind    string `json:"kind,omitempty"`
	URI     string `json:"uri,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// ToFactView projects evidence into the envelope's compact fact form.
func ToFactView(e Evidence) FactView {
	src := e.Source.Tool
	if src == "" {
		src = e.Source.Origin
	}
	return FactView{ID: e.ID, Claim: e.Claim, Confidence: e.Confidence, Source: src, Kind: e.Kind}
}

// ToHypView projects a hypothesis into the envelope's compact form.
func ToHypView(h Hypothesis) HypView {
	return HypView{ID: h.ID, Statement: h.Statement, Confidence: h.Confidence, Status: h.Status}
}
