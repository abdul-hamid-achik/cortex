package domain

// Plan is the planning-gate payload stored at plan.json (SPEC §10.2
// cortex_plan). It bundles the hypotheses, the declared change boundary, and
// the verification requirements a task commits to before it may enter changing.
type Plan struct {
	Hypotheses           []Hypothesis   `json:"hypotheses"`
	ChangeBoundary       ChangeBoundary `json:"changeBoundary"`
	VerificationRequired []string       `json:"verificationRequired"`
	Uncertainty          string         `json:"uncertainty,omitempty"`
}

// Validate enforces the planning gate (SPEC §13.1): at least one hypothesis,
// each with a disproof path, and an explicit statement of uncertainty.
func (p Plan) Validate() error {
	if len(p.Hypotheses) == 0 {
		return errValidation("plan has no hypotheses")
	}
	for _, h := range p.Hypotheses {
		if err := h.Validate(); err != nil {
			return err
		}
	}
	if p.Uncertainty == "" {
		return errValidation("plan must state uncertainty explicitly")
	}
	return nil
}
