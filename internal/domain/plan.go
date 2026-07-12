package domain

import "strings"

// Plan is the planning-gate payload stored at plan.json (SPEC §10.2
// cortex_plan). It bundles the hypotheses, the declared change boundary, and
// the verification requirements a task commits to before it may enter changing.
type Plan struct {
	Hypotheses           []Hypothesis   `json:"hypotheses"`
	ChangeBoundary       ChangeBoundary `json:"changeBoundary"`
	VerificationRequired []string       `json:"verificationRequired"`
	Uncertainty          string         `json:"uncertainty,omitempty"`
}

// KnownVerificationRequirement reports whether a verifier label has a defined
// kernel meaning. v0.1 intentionally keeps this closed: accepting an arbitrary
// string would create a requirement that verify/status cannot satisfy reliably.
func KnownVerificationRequirement(requirement string) bool {
	if strings.HasPrefix(requirement, "command:") && strings.TrimPrefix(requirement, "command:") != "" {
		return true
	}
	switch requirement {
	case "codemap_review", "cairntrace_flow", "glyphrun_flow", "fcheap_artifact", "tvault_capability":
		return true
	default:
		return false
	}
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
	if len(p.ChangeBoundary.Symbols) > 0 && len(p.ChangeBoundary.Files) == 0 {
		return errValidation("symbol-only change boundaries are not supported yet; include the owning file paths so scope drift can be checked")
	}
	for _, requirement := range p.VerificationRequired {
		if !KnownVerificationRequirement(requirement) {
			return errValidation("unknown verification requirement " + requirement)
		}
	}
	return nil
}
