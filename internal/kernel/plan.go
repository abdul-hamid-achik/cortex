package kernel

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
)

// PlanInput parameterizes Plan. It is a planning gate,
// not a code generator.
type PlanInput struct {
	TaskID         string
	Hypotheses     []HypothesisInput
	ChangeBoundary domain.ChangeBoundary
	Verification   []string
	Uncertainty    string
	// TimeoutOverrides maps a tool name to a per-task timeout.
	TimeoutOverrides map[string]string
}

// HypothesisInput is the model-facing hypothesis shape (disproveBy is free text
// or a structured contract; either satisfies the disproof requirement).
type HypothesisInput struct {
	Statement  string
	Supports   []string
	Confidence string
	DisproveBy string
}

// Plan stores hypotheses, the change boundary, and the verification plan, then
// gates the transition into a changing/verifying-ready state. It rejects any
// plan whose hypotheses lack a disproof path.
func (k *Kernel) Plan(in PlanInput) (domain.Envelope, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	if c.Status != domain.PhaseInvestigating && c.Status != domain.PhasePlanned {
		return errEnvelope(in.TaskID, fmt.Sprintf("cannot plan in phase %q; investigate first", c.Status)), nil
	}
	if len(in.Hypotheses) == 0 {
		return errEnvelope(in.TaskID, "a plan needs at least one hypothesis with a disproof path"), nil
	}
	if len(in.Hypotheses) > maxPlanHypotheses {
		return errEnvelope(in.TaskID, fmt.Sprintf("plan has more than %d hypotheses", maxPlanHypotheses)), nil
	}
	if textExceeds(strings.TrimSpace(in.Uncertainty), maxRecordTextBytes) {
		return errEnvelope(in.TaskID, fmt.Sprintf("plan uncertainty exceeds %d bytes", maxRecordTextBytes)), nil
	}
	boundary, err := normalizeBoundary(in.ChangeBoundary)
	if err != nil {
		return errEnvelope(in.TaskID, "plan rejected: "+err.Error()), nil
	}
	boundary, err = k.sanitizeBoundary(boundary)
	if err != nil {
		return errEnvelope(in.TaskID, "plan rejected: "+err.Error()), nil
	}
	if len(boundary.Symbols) > 0 && len(boundary.Files) == 0 {
		return errEnvelope(in.TaskID, "plan rejected: symbol-only change boundaries are not supported yet; include the owning file paths so scope drift can be checked"), nil
	}
	timeouts, err := normalizeTimeoutOverrides(in.TimeoutOverrides)
	if err != nil {
		return errEnvelope(in.TaskID, "plan rejected: "+err.Error()), nil
	}

	hyps := make([]domain.Hypothesis, 0, len(in.Hypotheses))
	for _, h := range in.Hypotheses {
		if textExceeds(strings.TrimSpace(h.Statement), maxRecordTextBytes) || textExceeds(strings.TrimSpace(h.DisproveBy), maxRecordTextBytes) {
			return errEnvelope(in.TaskID, fmt.Sprintf("hypothesis statement and disproof must each be at most %d bytes", maxRecordTextBytes)), nil
		}
		if len(h.Supports) > maxHypothesisSupports {
			return errEnvelope(in.TaskID, fmt.Sprintf("hypothesis has more than %d evidence supports", maxHypothesisSupports)), nil
		}
		confidence, err := normalizeHypothesisConfidence(h.Confidence)
		if err != nil {
			return errEnvelope(in.TaskID, "plan rejected: "+err.Error()), nil
		}
		supports := dedupeStr(h.Supports)
		for _, evidenceID := range supports {
			if _, err := k.store.GetEvidence(c.ID, evidenceID); err != nil {
				return errEnvelope(in.TaskID, fmt.Sprintf("plan rejected: hypothesis support %q is not evidence in task %s", evidenceID, c.ID)), nil
			}
		}
		hyp := domain.Hypothesis{
			ID:         ids.New("hyp"),
			Statement:  k.red.String(strings.TrimSpace(h.Statement)),
			Supports:   supports,
			Confidence: confidence,
			DisproveBy: domain.Disproof{Note: k.red.String(strings.TrimSpace(h.DisproveBy))},
			Status:     domain.HypActive,
		}
		if err := hyp.Validate(); err != nil {
			// A rejected plan preserves the investigating phase so the model can
			// supply a disproof path and retry (the planning gate rejects
			// plans with no disproof path).
			return errEnvelope(in.TaskID, "plan rejected: "+err.Error()), nil
		}
		hyps = append(hyps, hyp)
	}

	// A change task must declare a boundary before it can mutate.
	if c.Mode == domain.ModeChange && !boundary.Declared() {
		return errEnvelope(in.TaskID, "plan rejected: a change task must declare a change boundary (files and/or symbols)"), nil
	}

	verification := in.Verification
	if len(verification) == 0 {
		verification = k.defaultVerification(c.Surfaces)
	}
	verification, err = k.normalizeVerificationRequirements(verification)
	if err != nil {
		return errEnvelope(in.TaskID, "plan rejected: "+err.Error()), nil
	}
	if c.Mode == domain.ModeChange && (c.Risk == "medium" || c.Risk == "high") {
		verification = appendUniqueRequirement(verification, "codemap_review")
	}

	plan := domain.Plan{
		Hypotheses:           hyps,
		ChangeBoundary:       boundary,
		VerificationRequired: verification,
		Uncertainty:          k.red.String(strings.TrimSpace(in.Uncertainty)),
	}
	if err := plan.Validate(); err != nil {
		return errEnvelope(in.TaskID, "plan rejected: "+err.Error()), nil
	}

	// Commit the case, plan, and hypothesis snapshots under one revision-guarded
	// task transaction. A losing concurrent plan cannot overwrite the winner's
	// companion files before discovering its stale case revision.
	c.ChangeBoundary = boundary
	c.VerificationRequired = verification
	c.TimeoutOverrides = timeouts
	from := c.Status
	if c.Status != domain.PhasePlanned {
		if !domain.CanTransition(c.Status, domain.PhasePlanned) {
			return errEnvelope(c.ID, domain.ErrIllegalTransition{From: c.Status, To: domain.PhasePlanned}.Error()), nil
		}
		c.Status = domain.PhasePlanned
	}
	if err := k.store.CommitPlan(c, plan, hyps); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	if from != c.Status {
		k.recordPhase(c.ID, from, c.Status)
	}
	env := domain.Envelope{
		OK:     true,
		TaskID: c.ID,
		Phase:  c.Status,
		Summary: fmt.Sprintf("plan accepted: %s, boundary of %s, %s required",
			pluralizeGeneric(len(hyps), "hypothesis", "hypotheses"),
			boundarySummary(boundary),
			pluralizeGeneric(len(verification), "verifier", "verifiers")),
		NextActions: []string{
			"cortex begin-change — claim bounded change ownership before editing",
			"make your edits within the declared boundary — expand the plan if scope changes",
			"cortex verify — run the required verifiers and check for scope drift",
		},
		RawAvailable: false,
	}
	k.attachStructuredActions(&env, c)
	// A change task should have evidence supporting each hypothesis
	// before it enters changing. This is surfaced as a warning (not a hard
	// gate) so a hypothesis can be recorded before formal evidence exists, but
	// the gap is visible to the model.
	if c.Mode == domain.ModeChange {
		for _, h := range hyps {
			if len(h.Supports) == 0 {
				env.Warnings = append(env.Warnings, fmt.Sprintf("hypothesis %q has no supporting evidence — investigate to gather evidence before changing", h.ID))
			}
		}
	}
	for _, h := range hyps {
		env.Hypotheses = append(env.Hypotheses, domain.ToHypView(h))
	}
	return env, nil
}

func normalizeHypothesisConfidence(value string) (domain.Confidence, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		normalized = string(domain.ConfidenceLow)
	}
	confidence := domain.Confidence(normalized)
	switch confidence {
	case domain.ConfidenceHigh, domain.ConfidenceMedium, domain.ConfidenceLow, domain.ConfidenceUnknown:
		return confidence, nil
	default:
		return "", fmt.Errorf("hypothesis confidence must be one of high, medium, low, or unknown")
	}
}

func appendUniqueRequirement(requirements []string, required string) []string {
	for _, existing := range requirements {
		if existing == required {
			return requirements
		}
	}
	return append([]string{required}, requirements...)
}

func (k *Kernel) sanitizeBoundary(boundary domain.ChangeBoundary) (domain.ChangeBoundary, error) {
	for _, value := range append(append([]string(nil), boundary.Files...), boundary.Symbols...) {
		if k.red.Detected(value) {
			return domain.ChangeBoundary{}, fmt.Errorf("change boundary paths/symbols must not contain secret-shaped text")
		}
	}
	for i, raw := range boundary.Files {
		cleaned := filepath.Clean(raw)
		if filepath.IsAbs(cleaned) {
			relative, err := filepath.Rel(k.cfg.Workspace, cleaned)
			if err != nil {
				return domain.ChangeBoundary{}, fmt.Errorf("change boundary file %q cannot be made workspace-relative", raw)
			}
			cleaned = relative
		}
		cleaned = filepath.ToSlash(cleaned)
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return domain.ChangeBoundary{}, fmt.Errorf("change boundary file %q is outside the workspace", raw)
		}
		boundary.Files[i] = strings.TrimPrefix(cleaned, "./")
	}
	boundary.Reason = k.red.String(boundary.Reason)
	return boundary, nil
}

var knownTimeoutTools = map[string]bool{
	"git": true, "codemap": true, "vecgrep": true, "cairntrace": true,
	"glyphrun": true, "fcheap": true, "vidtrace": true, "tvault": true,
	"veclite": true, "command": true,
}

func normalizeTimeoutOverrides(overrides map[string]string) (map[string]string, error) {
	if len(overrides) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(overrides))
	for tool, raw := range overrides {
		name := strings.ToLower(strings.TrimSpace(tool))
		if !knownTimeoutTools[name] {
			return nil, fmt.Errorf("unknown timeout override tool %q", tool)
		}
		durationText := strings.TrimSpace(raw)
		duration, err := time.ParseDuration(durationText)
		if err != nil || duration <= 0 {
			return nil, fmt.Errorf("timeout override for %s must be a positive duration (got %q)", name, raw)
		}
		out[name] = durationText
	}
	return out, nil
}

func (k *Kernel) normalizeVerificationRequirements(requirements []string) ([]string, error) {
	out := make([]string, 0, len(requirements))
	seen := make(map[string]bool, len(requirements))
	for _, raw := range requirements {
		requirement := strings.ToLower(strings.TrimSpace(raw))
		if _, configured := k.cfg.Verifiers[requirement]; configured {
			requirement = "command:" + requirement
		}
		if strings.HasPrefix(requirement, "command:") {
			name := strings.TrimPrefix(requirement, "command:")
			if _, configured := k.cfg.Verifiers[name]; !configured {
				return nil, fmt.Errorf("unknown configured command verifier %q", name)
			}
		} else if !domain.KnownVerificationRequirement(requirement) {
			return nil, fmt.Errorf("unknown verification requirement %q", raw)
		}
		if !seen[requirement] {
			seen[requirement] = true
			out = append(out, requirement)
		}
	}
	return out, nil
}

func normalizeBoundary(boundary domain.ChangeBoundary) (domain.ChangeBoundary, error) {
	if len(boundary.Files) > maxBoundaryEntries || len(boundary.Symbols) > maxBoundaryEntries {
		return domain.ChangeBoundary{}, fmt.Errorf("change boundary supports at most %d files and %d symbols", maxBoundaryEntries, maxBoundaryEntries)
	}
	if textExceeds(strings.TrimSpace(boundary.Reason), maxRecordTextBytes) {
		return domain.ChangeBoundary{}, fmt.Errorf("change boundary reason exceeds %d bytes", maxRecordTextBytes)
	}
	files, err := normalizeBoundaryEntries("file", boundary.Files)
	if err != nil {
		return domain.ChangeBoundary{}, err
	}
	symbols, err := normalizeBoundaryEntries("symbol", boundary.Symbols)
	if err != nil {
		return domain.ChangeBoundary{}, err
	}
	boundary.Files = files
	boundary.Symbols = symbols
	boundary.Reason = strings.TrimSpace(boundary.Reason)
	return boundary, nil
}

func normalizeBoundaryEntries(kind string, entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return nil, fmt.Errorf("change boundary contains an empty %s", kind)
		}
		if textExceeds(entry, maxLocatorBytes) {
			return nil, fmt.Errorf("change boundary %s exceeds %d bytes", kind, maxLocatorBytes)
		}
		if !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	return out, nil
}

// defaultVerification derives a verifier list from the task's surfaces when the
// model doesn't supply one, preserving an explicit claim-to-proof mapping.
func (k *Kernel) defaultVerification(surfaces []domain.Surface) []string {
	out := []string{"codemap_review"}
	for _, s := range surfaces {
		switch s {
		case domain.SurfaceBrowser:
			out = append(out, "cairntrace_flow")
		case domain.SurfaceTerminal:
			out = append(out, "glyphrun_flow")
		case domain.SurfaceArtifact:
			out = append(out, "fcheap_artifact")
		case domain.SurfaceSecret:
			out = append(out, "tvault_capability")
		}
	}
	configured := make([]string, 0, len(k.cfg.Verifiers))
	for name := range k.cfg.Verifiers {
		configured = append(configured, "command:"+name)
	}
	sort.Strings(configured)
	out = append(out, configured...)
	return dedupeStr(out)
}

func boundarySummary(b domain.ChangeBoundary) string {
	if !b.Declared() {
		return "no files (investigation only)"
	}
	return fmt.Sprintf("%d file(s) / %d symbol(s)", len(b.Files), len(b.Symbols))
}

func pluralizeGeneric(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}
