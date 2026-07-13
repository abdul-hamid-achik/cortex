package domain

// HypothesisStatus tracks a hypothesis through the investigation.
type HypothesisStatus string

const (
	HypActive     HypothesisStatus = "active"
	HypChallenged HypothesisStatus = "challenged"
	HypRejected   HypothesisStatus = "rejected"
	HypConfirmed  HypothesisStatus = "confirmed"
)

// Disproof names the test that would falsify a hypothesis. A
// hypothesis without a disproof path cannot pass the planning gate.
type Disproof struct {
	Kind     string `json:"kind,omitempty"`     // behavioral_run, terminal_run, unit_test, …
	Tool     string `json:"tool,omitempty"`     // cairntrace, glyphrun, …
	Contract string `json:"contract,omitempty"` // named spec/contract to run
	Note     string `json:"note,omitempty"`     // free-form disproof description
}

// Declared reports whether a disproof path has been specified in any form.
func (d Disproof) Declared() bool {
	return d.Kind != "" || d.Tool != "" || d.Contract != "" || d.Note != ""
}

// Hypothesis is a falsifiable proposed explanation.
type Hypothesis struct {
	ID         string           `json:"id"`
	Statement  string           `json:"statement"`
	Supports   []string         `json:"supports,omitempty"` // evidence IDs
	Confidence Confidence       `json:"confidence"`
	DisproveBy Disproof         `json:"disproveBy"`
	Status     HypothesisStatus `json:"status"`
}

// Validate enforces the planning invariant that a hypothesis must state both a
// claim and how it could be disproved.
func (h Hypothesis) Validate() error {
	if h.Statement == "" {
		return errValidation("hypothesis has no statement")
	}
	if !h.DisproveBy.Declared() {
		return errValidation("hypothesis " + h.ID + " has no disproof path (set disproveBy)")
	}
	return nil
}
