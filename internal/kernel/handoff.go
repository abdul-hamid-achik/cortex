package kernel

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

const (
	maxHandoffEvidence = 20
	// maxHandoffBytes is a hard JSON budget for a transfer packet. Handoffs are
	// often injected directly into another model's context, so count-only bounds
	// are insufficient when one durable claim or decision contains a large body.
	maxHandoffBytes = 128 << 10
)

type handoffBudget struct {
	text, hypotheses, receipts, decisions, options int
	boundary, children, actions, warnings, refs    int
}

var handoffBudgets = []handoffBudget{
	{2048, 48, 48, 24, 12, 96, 64, 12, 32, 48},
	{1024, 24, 24, 12, 8, 64, 32, 8, 24, 32},
	{512, 12, 12, 4, 6, 32, 16, 4, 16, 16},
	{256, 6, 6, 1, 4, 16, 8, 2, 8, 8},
}

// Handoff is a bounded, provenance-preserving transfer packet for another
// agent or person. It contains current state and evidence, not a transcript or
// unbounded raw tool output.
type Handoff struct {
	SchemaVersion int                         `json:"schemaVersion"`
	GeneratedAt   time.Time                   `json:"generatedAt"`
	TaskID        string                      `json:"taskId"`
	Revision      uint64                      `json:"revision"`
	Goal          string                      `json:"goal"`
	Phase         domain.Phase                `json:"phase"`
	Mode          domain.Mode                 `json:"mode"`
	Risk          string                      `json:"risk"`
	Actor         string                      `json:"actor,omitempty"`
	ParentTaskID  string                      `json:"parentTaskId,omitempty"`
	ChildTaskIDs  []string                    `json:"childTaskIds,omitempty"`
	ChangeLease   *domain.ChangeLease         `json:"changeLease,omitempty"`
	Workspace     domain.Workspace            `json:"workspace"`
	Boundary      domain.ChangeBoundary       `json:"boundary,omitempty"`
	Plan          *domain.Plan                `json:"plan,omitempty"`
	Hypotheses    []domain.Hypothesis         `json:"hypotheses,omitempty"`
	Evidence      []domain.FactView           `json:"evidence,omitempty"`
	Verification  VerificationAssessment      `json:"verification"`
	Receipts      []domain.VerificationRecord `json:"receipts,omitempty"`
	Decisions     []domain.Decision           `json:"decisions,omitempty"`
	Actions       []domain.NextAction         `json:"actions,omitempty"`
	Warnings      []string                    `json:"warnings,omitempty"`
}

// BuildHandoff creates a bounded packet from the same canonical projection
// used by show and Studio.
func BuildHandoff(taskID string, now time.Time) (Handoff, error) {
	return BuildHandoffIn("", taskID, now)
}

// BuildHandoffIn creates a handoff with an explicit workspace fallback for
// repo-local/custom case stores.
func BuildHandoffIn(workspace, taskID string, now time.Time) (Handoff, error) {
	slug, store, err := LocateSessionIn(workspace, taskID)
	if err != nil {
		return Handoff{}, err
	}
	snapshot, err := store.HandoffSnapshot(taskID, maxHandoffEvidence)
	if err != nil {
		return Handoff{}, err
	}
	v := sessionViewFromSnapshot(slug, snapshot)
	c := v.Case
	receipts, omittedReceipts := safeHandoffReceipts(v.currentReceipts)
	decisions, omittedDecisions, sensitivePending := safeHandoffDecisions(v.Decisions)
	h := Handoff{
		SchemaVersion: 1, GeneratedAt: now.UTC(), TaskID: c.ID, Revision: c.Revision, Goal: c.Goal,
		Phase: c.Status, Mode: c.Mode, Risk: c.Risk, Actor: c.Actor,
		ParentTaskID: c.ParentTaskID, ChildTaskIDs: append([]string(nil), c.ChildTaskIDs...),
		ChangeLease: cloneChangeLease(c.ChangeLease), Workspace: c.Workspace,
		Boundary: c.ChangeBoundary, Plan: v.Plan, Hypotheses: v.Hypotheses,
		Verification: v.VerificationAssessment, Receipts: receipts,
		Decisions: decisions, Actions: safeHandoffActions(v.Actions, sensitivePending),
	}
	normalEvidence := make([]domain.Evidence, 0, len(v.Evidence))
	omittedEvidence := snapshot.SensitiveEvidenceOmitted
	for _, evidence := range v.Evidence {
		if evidence.Sensitivity == domain.SensitivitySensitive {
			omittedEvidence++
			continue
		}
		normalEvidence = append(normalEvidence, evidence)
	}
	start := 0
	if snapshot.ShareableEvidenceTotal > maxHandoffEvidence {
		h.Warnings = append(h.Warnings, fmt.Sprintf("evidence bounded to the %d most recent of %d shareable records", maxHandoffEvidence, snapshot.ShareableEvidenceTotal))
	}
	if len(normalEvidence) > maxHandoffEvidence {
		start = len(normalEvidence) - maxHandoffEvidence
	}
	for _, evidence := range normalEvidence[start:] {
		h.Evidence = append(h.Evidence, domain.ToFactView(evidence))
	}
	if omittedEvidence+omittedReceipts+omittedDecisions > 0 {
		h.Warnings = append(h.Warnings, fmt.Sprintf(
			"sensitive records omitted from handoff: %d evidence, %d receipts, %d decisions",
			omittedEvidence, omittedReceipts, omittedDecisions,
		))
	}
	if c.Status == domain.PhaseNeedsHumanDecision {
		h.Warnings = append(h.Warnings, "task is paused for a human decision; answer it before continuing")
	}
	if v.VerificationAssessment.Outcome != VerificationVerified {
		h.Warnings = append(h.Warnings, "verification outcome is "+string(v.VerificationAssessment.Outcome))
	}
	if len(v.StaleVerification) > 0 {
		h.Warnings = append(h.Warnings, fmt.Sprintf("%d verification receipt(s) are stale for the current workspace", len(v.StaleVerification)))
	}
	h.Warnings = append(h.Warnings, v.VerificationWarnings...)
	return boundHandoff(h), nil
}

func latestHandoffReceipts(receipts []domain.VerificationRecord) []domain.VerificationRecord {
	current := currentVerificationReceipts(receipts)
	verifiers := latestReceipts(current, domain.VerificationPurposeVerifierRun)
	claims := currentNamedClaims(receipts, current)
	return append(verifiers, claims...)
}

func safeHandoffReceipts(receipts []domain.VerificationRecord) ([]domain.VerificationRecord, int) {
	current := latestHandoffReceipts(receipts)
	out := make([]domain.VerificationRecord, 0, len(current))
	omitted := 0
	for _, receipt := range current {
		if receipt.Sensitive {
			omitted++
			continue
		}
		out = append(out, receipt)
	}
	return out, omitted
}

func safeHandoffDecisions(decisions []domain.Decision) ([]domain.Decision, int, bool) {
	out := make([]domain.Decision, 0, len(decisions))
	omitted := 0
	pending := false
	for _, decision := range decisions {
		if !decision.Sensitive {
			out = append(out, decision)
			continue
		}
		omitted++
		if decision.Status != domain.DecisionPending {
			continue
		}
		pending = true
		// Preserve the non-secret continuation identity without exporting the
		// sensitive question, requester, labels, or consequences.
		out = append(out, domain.Decision{
			ID: decision.ID, RequestedAt: decision.RequestedAt,
			Status: decision.Status, Sensitive: true,
		})
	}
	return out, omitted, pending
}

func safeHandoffActions(actions []domain.NextAction, sensitivePending bool) []domain.NextAction {
	out := append([]domain.NextAction(nil), actions...)
	if !sensitivePending {
		return out
	}
	for i := range out {
		if out[i].Tool != "cortex_request_decision" {
			continue
		}
		args := make(map[string]any, 2)
		for _, key := range []string{"taskId", "workspace"} {
			if value, ok := out[i].Arguments[key]; ok {
				args[key] = value
			}
		}
		out[i].Arguments = args
		out[i].Command = ""
		out[i].Inputs = []string{"question", "options", "requester"}
		out[i].BlockedBy = append(out[i].BlockedBy, "sensitive decision details omitted from handoff")
	}
	return out
}

// RenderHandoffMarkdown produces a portable human-readable handoff. Raw tool
// output is intentionally omitted; evidence and artifact IDs remain followable.
func RenderHandoffMarkdown(h Handoff) string {
	h = boundHandoff(h)
	var b strings.Builder
	fmt.Fprintf(&b, "# Cortex handoff: %s\n\n", h.TaskID)
	fmt.Fprintf(&b, "**Goal:** %s\n\n", singleLine(h.Goal))
	fmt.Fprintf(&b, "**State:** %s · %s · risk %s · verification %s\n\n", h.Phase, h.Mode, h.Risk, h.Verification.Outcome)
	fmt.Fprintf(&b, "**Revision:** %d", h.Revision)
	if h.Actor != "" {
		fmt.Fprintf(&b, " · actor `%s`", singleLine(h.Actor))
	}
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "**Workspace:** %s", h.Workspace.Repository)
	if h.Workspace.Branch != "" {
		fmt.Fprintf(&b, " on `%s`", h.Workspace.Branch)
	}
	b.WriteString("\n\n")
	if h.ParentTaskID != "" || len(h.ChildTaskIDs) > 0 || h.ChangeLease != nil {
		b.WriteString("## Coordination\n\n")
		if h.ParentTaskID != "" {
			fmt.Fprintf(&b, "- Parent: `%s`\n", h.ParentTaskID)
		}
		for _, child := range h.ChildTaskIDs {
			fmt.Fprintf(&b, "- Child: `%s`\n", child)
		}
		if h.ChangeLease != nil {
			fmt.Fprintf(&b, "- Change lease: `%s` until %s", singleLine(h.ChangeLease.Actor), h.ChangeLease.ExpiresAt.Format(time.RFC3339))
			if h.ChangeLease.ReleasedAt != nil {
				fmt.Fprintf(&b, " (released %s)", h.ChangeLease.ReleasedAt.Format(time.RFC3339))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if h.Plan != nil {
		b.WriteString("## Plan\n\n")
		if h.Plan.Uncertainty != "" {
			fmt.Fprintf(&b, "**Uncertainty:** %s\n\n", singleLine(h.Plan.Uncertainty))
		}
		if len(h.Plan.VerificationRequired) > 0 {
			b.WriteString("**Required verification:**\n\n")
			for _, requirement := range h.Plan.VerificationRequired {
				fmt.Fprintf(&b, "- `%s`\n", requirement)
			}
			b.WriteString("\n")
		}
	}
	if h.Boundary.Declared() {
		b.WriteString("## Declared boundary\n\n")
		for _, file := range h.Boundary.Files {
			fmt.Fprintf(&b, "- `%s`\n", file)
		}
		for _, symbol := range h.Boundary.Symbols {
			fmt.Fprintf(&b, "- symbol `%s`\n", symbol)
		}
		if h.Boundary.Reason != "" {
			fmt.Fprintf(&b, "\n%s\n", singleLine(h.Boundary.Reason))
		}
		b.WriteString("\n")
	}
	if len(h.Hypotheses) > 0 {
		b.WriteString("## Hypotheses\n\n")
		for _, hypothesis := range h.Hypotheses {
			fmt.Fprintf(&b, "- **%s** `%s`: %s", hypothesis.Status, hypothesis.ID, singleLine(hypothesis.Statement))
			if hypothesis.DisproveBy.Note != "" {
				fmt.Fprintf(&b, " — disprove with: %s", singleLine(hypothesis.DisproveBy.Note))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(h.Evidence) > 0 {
		b.WriteString("## Evidence\n\n")
		for _, evidence := range h.Evidence {
			fmt.Fprintf(&b, "- `%s` (%s, %s): %s\n", evidence.ID, evidence.Confidence, evidence.Source, singleLine(evidence.Claim))
		}
		b.WriteString("\n")
	}
	b.WriteString("## Verification\n\n")
	fmt.Fprintf(&b, "Outcome: **%s**\n", h.Verification.Outcome)
	if len(h.Verification.MissingRequired) > 0 {
		fmt.Fprintf(&b, "\nMissing required: %s\n", strings.Join(h.Verification.MissingRequired, ", "))
	}
	if len(h.Verification.FailedClaims) > 0 {
		fmt.Fprintf(&b, "\nFailed claims: %s\n", strings.Join(h.Verification.FailedClaims, "; "))
	}
	if len(h.Verification.NonPassingClaims) > 0 {
		fmt.Fprintf(&b, "\nNon-passing claims: %s\n", strings.Join(h.Verification.NonPassingClaims, "; "))
	}
	b.WriteString("\n")
	if len(h.Receipts) > 0 {
		b.WriteString("### Current receipts\n\n")
		for _, receipt := range h.Receipts {
			fmt.Fprintf(&b, "- **%s** `%s` (%s): %s", receipt.Status, receipt.ID, receipt.Surface, singleLine(receipt.Claim))
			if receipt.Tool != "" {
				fmt.Fprintf(&b, " — verifier `%s`", receipt.Tool)
			}
			if receipt.Requirement != "" {
				fmt.Fprintf(&b, "; requirement `%s`", receipt.Requirement)
			}
			if receipt.Contract != "" {
				fmt.Fprintf(&b, "; contract `%s`", receipt.Contract)
			}
			if receipt.Binding != "" {
				fmt.Fprintf(&b, "; %s", receipt.Binding)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(h.Decisions) > 0 {
		b.WriteString("## Decisions\n\n")
		for _, decision := range h.Decisions {
			fmt.Fprintf(&b, "- **%s** `%s`: %s", decision.Status, decision.ID, singleLine(decision.Question))
			if decision.Status == domain.DecisionAnswered {
				fmt.Fprintf(&b, " — selected `%s` by %s", decision.Answer, singleLine(decision.Responder))
			}
			b.WriteString("\n")
			if decision.Status != domain.DecisionPending {
				continue
			}
			for _, option := range decision.Options {
				fmt.Fprintf(&b, "  - `%s` — %s: %s\n", option.ID, singleLine(option.Label), singleLine(option.Consequence))
			}
		}
		b.WriteString("\n")
	}
	if len(h.Warnings) > 0 {
		b.WriteString("## Warnings\n\n")
		for _, warning := range h.Warnings {
			fmt.Fprintf(&b, "- %s\n", singleLine(warning))
		}
		b.WriteString("\n")
	}
	if len(h.Actions) > 0 {
		b.WriteString("## Next actions\n\n")
		for _, action := range h.Actions {
			label := action.Tool
			if action.Command != "" {
				label = "`" + action.Command + "`"
			}
			fmt.Fprintf(&b, "- %s — %s", label, singleLine(action.Reason))
			if len(action.Inputs) > 0 {
				fmt.Fprintf(&b, "; needs: %s", strings.Join(action.Inputs, ", "))
			}
			if len(action.BlockedBy) > 0 {
				fmt.Fprintf(&b, "; blocked by: %s", strings.Join(action.BlockedBy, ", "))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func singleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// boundHandoff projects every free-form field as well as collection counts,
// then tightens the projection until the serialized packet fits its hard
// context budget. It operates on a JSON clone so rendering cannot mutate a
// caller's canonical case projection through shared slices or maps.
func boundHandoff(h Handoff) Handoff {
	original, err := json.Marshal(h)
	if err != nil {
		return minimalHandoff(h)
	}
	var out Handoff
	if err := json.Unmarshal(original, &out); err != nil {
		return minimalHandoff(h)
	}
	changed := false
	for _, budget := range handoffBudgets {
		if projectHandoff(&out, budget) {
			changed = true
		}
		encoded, err := json.Marshal(out)
		if err == nil && len(encoded) <= maxHandoffBytes {
			if changed || len(original) > maxHandoffBytes {
				appendHandoffWarning(&out, fmt.Sprintf("handoff content bounded to %d KiB for portable agent transfer", maxHandoffBytes>>10))
				// The warning itself may cross a near-exact boundary. The next
				// budget will tighten it; the final profile has ample headroom.
				encoded, _ = json.Marshal(out)
				if len(encoded) > maxHandoffBytes {
					continue
				}
			}
			return out
		}
	}
	out = minimalHandoff(out)
	appendHandoffWarning(&out, fmt.Sprintf("handoff content bounded to %d KiB for portable agent transfer", maxHandoffBytes>>10))
	return out
}

func projectHandoff(h *Handoff, budget handoffBudget) bool {
	changed := false
	clip := func(value *string) {
		bounded, clipped := boundedUTF8(*value, budget.text)
		if clipped {
			*value = bounded + "…"
			changed = true
		}
	}
	clipStrings := func(values *[]string, limit int, newest bool) {
		if boundStringSlice(values, limit, newest, budget.text) {
			changed = true
		}
	}

	clip(&h.TaskID)
	clip(&h.Goal)
	clip(&h.Risk)
	clip(&h.Actor)
	clip(&h.ParentTaskID)
	clip(&h.Workspace.Root)
	clip(&h.Workspace.Repository)
	clip(&h.Workspace.Branch)
	clip(&h.Workspace.CommitBefore)
	clip(&h.Workspace.BaseRef)
	clipStrings(&h.ChildTaskIDs, budget.children, false)
	if h.ChangeLease != nil {
		clip(&h.ChangeLease.Actor)
	}
	clip(&h.Boundary.Reason)
	clipStrings(&h.Boundary.Files, budget.boundary, false)
	clipStrings(&h.Boundary.Symbols, budget.boundary, false)

	if h.Plan != nil {
		clip(&h.Plan.Uncertainty)
		clipStrings(&h.Plan.VerificationRequired, budget.refs, false)
		if boundHypotheses(&h.Plan.Hypotheses, budget.hypotheses, budget.refs, budget.text) {
			changed = true
		}
		if boundStringSlice(&h.Plan.ChangeBoundary.Files, budget.boundary, false, budget.text) ||
			boundStringSlice(&h.Plan.ChangeBoundary.Symbols, budget.boundary, false, budget.text) {
			changed = true
		}
		clip(&h.Plan.ChangeBoundary.Reason)
	}
	if boundHypotheses(&h.Hypotheses, budget.hypotheses, budget.refs, budget.text) {
		changed = true
	}
	if keepNewest(&h.Evidence, maxHandoffEvidence) {
		changed = true
	}
	for i := range h.Evidence {
		clip(&h.Evidence[i].ID)
		clip(&h.Evidence[i].Claim)
		clip(&h.Evidence[i].Source)
		clip(&h.Evidence[i].Actor)
		clip(&h.Evidence[i].Category)
		clipStrings(&h.Evidence[i].DerivedFrom, budget.refs, false)
	}
	clipStrings(&h.Verification.SatisfiedRequired, budget.refs, false)
	clipStrings(&h.Verification.MissingRequired, budget.refs, false)
	clipStrings(&h.Verification.NonPassingClaims, budget.refs, false)
	clipStrings(&h.Verification.FailedClaims, budget.refs, false)
	if keepNewest(&h.Receipts, budget.receipts) {
		changed = true
	}
	for i := range h.Receipts {
		boundReceipt(&h.Receipts[i], budget.refs, budget.text, &changed)
	}
	if boundDecisions(&h.Decisions, budget.decisions, budget.options, budget.text) {
		changed = true
	}
	if keepFirst(&h.Actions, budget.actions) {
		changed = true
	}
	for i := range h.Actions {
		clip(&h.Actions[i].Tool)
		clip(&h.Actions[i].Command)
		clip(&h.Actions[i].Reason)
		clipStrings(&h.Actions[i].Inputs, budget.refs, false)
		clipStrings(&h.Actions[i].BlockedBy, budget.refs, false)
		bounded, clipped := boundActionArguments(h.Actions[i].Arguments, budget.refs, budget.text)
		h.Actions[i].Arguments = bounded
		changed = changed || clipped
	}
	clipStrings(&h.Warnings, budget.warnings, false)
	return changed
}

func boundHypotheses(values *[]domain.Hypothesis, limit, refs, textLimit int) bool {
	changed := keepFirst(values, limit)
	for i := range *values {
		h := &(*values)[i]
		changed = clipHandoffString(&h.ID, textLimit) || changed
		changed = clipHandoffString(&h.Statement, textLimit) || changed
		changed = boundStringSlice(&h.Supports, refs, false, textLimit) || changed
		changed = clipHandoffString(&h.DisproveBy.Kind, textLimit) || changed
		changed = clipHandoffString(&h.DisproveBy.Tool, textLimit) || changed
		changed = clipHandoffString(&h.DisproveBy.Contract, textLimit) || changed
		changed = clipHandoffString(&h.DisproveBy.Note, textLimit) || changed
	}
	return changed
}

func boundReceipt(receipt *domain.VerificationRecord, refs, textLimit int, changed *bool) {
	for _, value := range []*string{
		&receipt.ID, &receipt.BatchID, &receipt.Claim, &receipt.Requirement,
		&receipt.ClaimID, &receipt.Actor, &receipt.Contract, &receipt.Tool,
		&receipt.VerifierVersion, &receipt.Artifact, &receipt.Revision,
		&receipt.DirtyDigest, &receipt.Notes,
	} {
		*changed = clipHandoffString(value, textLimit) || *changed
	}
	*changed = boundStringSlice(&receipt.Evidence, refs, false, textLimit) || *changed
}

func boundDecisions(values *[]domain.Decision, limit, options, textLimit int) bool {
	changed := false
	if len(*values) > limit {
		// Prefer the newest decisions, but never discard the newest pending
		// question that currently blocks continuation.
		pending := -1
		for i := len(*values) - 1; i >= 0; i-- {
			if (*values)[i].Status == domain.DecisionPending {
				pending = i
				break
			}
		}
		start := len(*values) - limit
		trimmed := append([]domain.Decision(nil), (*values)[start:]...)
		if pending >= 0 && pending < start {
			trimmed[0] = (*values)[pending]
		}
		*values = trimmed
		changed = true
	}
	for i := range *values {
		decision := &(*values)[i]
		for _, value := range []*string{
			&decision.ID, &decision.Question, &decision.Requester, &decision.Answer,
			&decision.Responder, &decision.EvidenceID,
		} {
			changed = clipHandoffString(value, textLimit) || changed
		}
		changed = keepFirst(&decision.Options, options) || changed
		for j := range decision.Options {
			changed = clipHandoffString(&decision.Options[j].ID, textLimit) || changed
			changed = clipHandoffString(&decision.Options[j].Label, textLimit) || changed
			changed = clipHandoffString(&decision.Options[j].Consequence, textLimit) || changed
		}
	}
	return changed
}

func boundActionArguments(values map[string]any, limit, textLimit int) (map[string]any, bool) {
	return boundArgumentMap(values, limit, textLimit, 0)
}

func boundArgumentMap(values map[string]any, limit, textLimit, depth int) (map[string]any, bool) {
	if len(values) == 0 {
		return values, false
	}
	priority := []string{"taskId", "workspace", "decisionId", "actor", "ttl", "resume"}
	keys := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, key := range priority {
		if _, ok := values[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	remaining := make([]string, 0, len(values))
	for key := range values {
		if !seen[key] {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	keys = append(keys, remaining...)
	changed := false
	if len(keys) > limit {
		keys = keys[:limit]
		changed = true
	}
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		bounded, clipped := boundJSONValue(values[key], limit, textLimit, depth+1)
		out[key] = bounded
		changed = changed || clipped
	}
	return out, changed
}

func boundJSONValue(value any, limit, textLimit, depth int) (any, bool) {
	if depth >= 4 {
		return "…", true
	}
	switch typed := value.(type) {
	case string:
		bounded, clipped := boundedUTF8(typed, textLimit)
		if clipped {
			bounded += "…"
		}
		return bounded, clipped
	case []any:
		changed := false
		if len(typed) > limit {
			typed = typed[:limit]
			changed = true
		}
		out := make([]any, len(typed))
		for i := range typed {
			out[i], changed = boundJSONValueMerge(typed[i], limit, textLimit, depth+1, changed)
		}
		return out, changed
	case map[string]any:
		return boundArgumentMap(typed, limit, textLimit, depth)
	default:
		return value, false
	}
}

func boundJSONValueMerge(value any, limit, textLimit, depth int, changed bool) (any, bool) {
	bounded, clipped := boundJSONValue(value, limit, textLimit, depth)
	return bounded, changed || clipped
}

func boundStringSlice(values *[]string, limit int, newest bool, textLimit int) bool {
	changed := false
	if newest {
		changed = keepNewest(values, limit)
	} else {
		changed = keepFirst(values, limit)
	}
	for i := range *values {
		changed = clipHandoffString(&(*values)[i], textLimit) || changed
	}
	return changed
}

func clipHandoffString(value *string, limit int) bool {
	bounded, clipped := boundedUTF8(*value, limit)
	if clipped {
		*value = bounded + "…"
	}
	return clipped
}

func keepFirst[T any](values *[]T, limit int) bool {
	if len(*values) <= limit {
		return false
	}
	*values = append([]T(nil), (*values)[:limit]...)
	return true
}

func keepNewest[T any](values *[]T, limit int) bool {
	if len(*values) <= limit {
		return false
	}
	*values = append([]T(nil), (*values)[len(*values)-limit:]...)
	return true
}

func appendHandoffWarning(h *Handoff, warning string) {
	for _, existing := range h.Warnings {
		if existing == warning {
			return
		}
	}
	h.Warnings = append(h.Warnings, warning)
}

func minimalHandoff(h Handoff) Handoff {
	out := Handoff{
		SchemaVersion: h.SchemaVersion, GeneratedAt: h.GeneratedAt,
		TaskID: h.TaskID, Revision: h.Revision, Goal: h.Goal,
		Phase: h.Phase, Mode: h.Mode, Risk: h.Risk, Actor: h.Actor,
		ParentTaskID: h.ParentTaskID, Workspace: h.Workspace,
		Verification: h.Verification,
	}
	if len(h.Decisions) > 0 {
		selected := h.Decisions[len(h.Decisions)-1]
		for i := len(h.Decisions) - 1; i >= 0; i-- {
			if h.Decisions[i].Status == domain.DecisionPending {
				selected = h.Decisions[i]
				break
			}
		}
		out.Decisions = append([]domain.Decision(nil), selected)
	}
	if len(h.Actions) > 0 {
		out.Actions = append([]domain.NextAction(nil), h.Actions[0])
	}
	out.Warnings = append([]string(nil), h.Warnings...)
	projectHandoff(&out, handoffBudgets[len(handoffBudgets)-1])
	return out
}
