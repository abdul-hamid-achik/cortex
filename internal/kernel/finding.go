package kernel

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

const (
	maxFindingRefs = 32
)

// FindingInput records one durable backlog item discovered while working.
type FindingInput struct {
	TaskID    string
	Kind      string // bug | improvement | feature | question
	Severity  string // low | medium | high (default medium)
	Title     string
	Detail    string
	Files     []string
	Symbols   []string
	Evidence  []string // evidence IDs in the owning case
	Actor     string
	Sensitive bool
}

// FindingStatusInput triages or dismisses a finding.
type FindingStatusInput struct {
	TaskID    string
	FindingID string
	Status    string // triaged | dismissed
	Reason    string
	Actor     string
}

// ConvertFindingInput opens a child case from a finding.
type ConvertFindingInput struct {
	TaskID    string
	FindingID string
	Actor     string
	Mode      string // default change
	Risk      string // default from severity
	Surfaces  []domain.Surface
}

// FindingCounts is the bounded backlog rollup status reports.
type FindingCounts struct {
	Total     int `json:"total"`
	Open      int `json:"open"`
	Triaged   int `json:"triaged"`
	Converted int `json:"converted"`
	Dismissed int `json:"dismissed"`
}

// FindingReport is the result of every finding operation.
type FindingReport struct {
	domain.Envelope
	Finding  *domain.Finding  `json:"finding,omitempty"`
	Findings []domain.Finding `json:"findings,omitempty"`
	Counts   FindingCounts    `json:"counts"`
	// ChildTaskID names the case a conversion opened.
	ChildTaskID string `json:"childTaskId,omitempty"`
}

func countFindings(findings []domain.Finding) FindingCounts {
	c := FindingCounts{Total: len(findings)}
	for _, f := range findings {
		switch f.Status {
		case domain.FindingOpen:
			c.Open++
		case domain.FindingTriaged:
			c.Triaged++
		case domain.FindingConverted:
			c.Converted++
		case domain.FindingDismissed:
			c.Dismissed++
		}
	}
	return c
}

func findingEnumAction(c *domain.CaseFile, field string, values []string) domain.NextAction {
	return domain.NextAction{
		Tool: "cortex_finding", Command: cortexCommand(c, "finding", "add", c.ID, "TITLE", "--"+field, strings.ToUpper(field)),
		Reason: "use a documented " + field, Arguments: knownActionArgs(c),
		Inputs: []string{field}, Candidates: map[string][]string{field: values},
	}
}

// RecordFinding appends a redacted, provenance-bearing finding to an active
// case. Evidence IDs must exist in the case so a finding can never cite proof
// it does not have.
func (k *Kernel) RecordFinding(in FindingInput) (FindingReport, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return FindingReport{Envelope: errEnvelope(in.TaskID, err.Error())}, nil
	}
	if c.Status.IsTerminal() {
		return FindingReport{Envelope: k.errEnvelopeForCase(c, fmt.Sprintf("cannot record a finding in terminal phase %q", c.Status))}, nil
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return FindingReport{Envelope: k.errEnvelopeActions(c.ID, "finding needs a title", domain.NextAction{
			Tool: "cortex_finding", Command: cortexCommand(c, "finding", "add", c.ID, "TITLE"),
			Reason: "state what was found in one line", Arguments: knownActionArgs(c), Inputs: []string{"title"},
		})}, nil
	}
	if textExceeds(title, maxLocatorBytes) {
		return FindingReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("finding title exceeds %d bytes", maxLocatorBytes))}, nil
	}
	if textExceeds(in.Detail, maxRecordTextBytes) {
		return FindingReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("finding detail exceeds %d bytes", maxRecordTextBytes))}, nil
	}
	kind := domain.FindingKind(strings.ToLower(strings.TrimSpace(in.Kind)))
	if kind == "" {
		kind = domain.FindingBug
	}
	if !kind.Valid() {
		return FindingReport{Envelope: k.errEnvelopeActions(c.ID, "finding kind must be one of: "+strings.Join(domain.FindingKinds, ", "), findingEnumAction(c, "kind", domain.FindingKinds))}, nil
	}
	severity := strings.ToLower(strings.TrimSpace(in.Severity))
	if severity == "" {
		severity = "medium"
	}
	switch severity {
	case "low", "medium", "high":
	default:
		return FindingReport{Envelope: k.errEnvelopeActions(c.ID, "finding severity must be low, medium, or high", findingEnumAction(c, "severity", domain.FindingSeverities))}, nil
	}
	if len(in.Files) > maxFindingRefs || len(in.Symbols) > maxFindingRefs || len(in.Evidence) > maxFindingRefs {
		return FindingReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("a finding accepts at most %d files, symbols, and evidence ids each", maxFindingRefs))}, nil
	}
	if textExceeds(strings.TrimSpace(in.Actor), maxStableIdentifierBytes) {
		return FindingReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("finding actor exceeds %d bytes", maxStableIdentifierBytes))}, nil
	}
	evidenceIDs, missing := k.knownEvidenceIDs(c.ID, in.Evidence)
	if len(missing) > 0 {
		return FindingReport{Envelope: k.errEnvelopeActions(c.ID, "finding cites evidence that does not exist in this case: "+strings.Join(missing, ", "),
			domain.NextAction{Tool: "cortex_finding", Command: cortexCommand(c, "finding", "add", c.ID, title),
				Reason: "cite only evidence ids recorded in this case", Arguments: knownActionArgs(c),
				Inputs: []string{"evidence"}, Candidates: map[string][]string{"evidence": k.evidenceIDCandidates(c.ID)}})}, nil
	}
	sensitive := in.Sensitive || k.red.Detected(title) || k.red.Detected(in.Detail)
	files := make([]string, 0, len(in.Files))
	for _, f := range in.Files {
		if f = strings.TrimSpace(f); f != "" {
			sensitive = sensitive || k.red.Detected(f)
			files = append(files, k.red.String(k.workspaceRelative(f)))
		}
	}
	symbols := make([]string, 0, len(in.Symbols))
	for _, s := range in.Symbols {
		if s = strings.TrimSpace(s); s != "" {
			sensitive = sensitive || k.red.Detected(s)
			symbols = append(symbols, k.red.String(s))
		}
	}
	now := k.now().UTC()
	f := domain.Finding{
		ID: ids.New("fnd"), TaskID: c.ID, Kind: kind, Severity: severity,
		Title: k.red.String(title), Detail: k.red.String(strings.TrimSpace(in.Detail)),
		Files: files, Symbols: symbols, Evidence: evidenceIDs, Status: domain.FindingOpen,
		Actor: k.red.String(strings.TrimSpace(in.Actor)), CreatedAt: now, UpdatedAt: now, Sensitive: sensitive,
	}
	if err := k.store.AppendFinding(c.ID, f); err != nil {
		return FindingReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	findings, _ := k.store.Findings(c.ID)
	rep := FindingReport{
		Envelope: domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status,
			Summary:     fmt.Sprintf("recorded %s finding %s (%s): %s", f.Kind, f.ID, f.Severity, clipStr(f.Title, 80)),
			NextActions: nextForPhase(c.Status)},
		Finding: &f, Counts: countFindings(findings),
	}
	rep.Actions = append(findingActions(c, f), rep.Actions...)
	k.attachStructuredActions(&rep.Envelope, c)
	k.checkpoint(c.ID)
	return rep, nil
}

// knownEvidenceIDs resolves the ids that exist in the case and reports the
// ones that do not.
func (k *Kernel) knownEvidenceIDs(taskID string, requested []string) (known, missing []string) {
	seen := map[string]bool{}
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := k.store.GetEvidence(taskID, id); err != nil {
			missing = append(missing, id)
			continue
		}
		known = append(known, id)
	}
	return known, missing
}

func findingActions(c *domain.CaseFile, f domain.Finding) []domain.NextAction {
	if f.Status.Terminal() {
		return nil
	}
	args := knownActionArgs(c)
	args["findingId"] = f.ID
	convert := domain.NextAction{
		Tool: "cortex_finding", Command: cortexCommand(c, "finding", "convert", c.ID, f.ID),
		Reason: "open a bounded child case whose acceptance criterion is this finding", Arguments: cloneArgs(args, "operation", "convert"),
		Inputs: []string{"actor"},
	}
	dismiss := domain.NextAction{
		Tool: "cortex_finding", Command: cortexCommand(c, "finding", "dismiss", c.ID, f.ID, "--reason", "REASON"),
		Reason: "record why this is not worth acting on (kept for recall)", Arguments: cloneArgs(args, "operation", "dismiss"),
		Inputs: []string{"reason"},
	}
	return []domain.NextAction{convert, dismiss}
}

func cloneArgs(args map[string]any, extra ...any) map[string]any {
	out := make(map[string]any, len(args)+len(extra)/2)
	for key, value := range args {
		out[key] = value
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if key, ok := extra[i].(string); ok {
			out[key] = extra[i+1]
		}
	}
	return out
}

// ListFindings returns a task's backlog, optionally filtered by status.
func (k *Kernel) ListFindings(taskID, status string) (FindingReport, error) {
	c, err := k.store.Load(taskID)
	if err != nil {
		return FindingReport{Envelope: errEnvelope(taskID, err.Error())}, nil
	}
	filter := domain.FindingStatus(strings.ToLower(strings.TrimSpace(status)))
	if filter != "" && !filter.Valid() {
		return FindingReport{Envelope: k.errEnvelopeActions(c.ID, "finding status filter must be one of: "+strings.Join(domain.FindingStatuses, ", "),
			domain.NextAction{Tool: "cortex_finding", Command: cortexCommand(c, "finding", "list", c.ID, "--status", "STATUS"),
				Reason: "use a documented status", Arguments: cloneArgs(knownActionArgs(c), "operation", "list"),
				Inputs: []string{"status"}, Candidates: map[string][]string{"status": domain.FindingStatuses}})}, nil
	}
	findings, err := k.store.Findings(c.ID)
	if err != nil {
		return FindingReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	counts := countFindings(findings)
	var out []domain.Finding
	for _, f := range findings {
		if filter == "" || f.Status == filter {
			out = append(out, f)
		}
	}
	rep := FindingReport{
		Envelope: domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status,
			Summary: fmt.Sprintf("%d finding(s) (%d open, %d triaged, %d converted, %d dismissed)", counts.Total, counts.Open, counts.Triaged, counts.Converted, counts.Dismissed)},
		Findings: out, Counts: counts,
	}
	for _, f := range out {
		if !f.Status.Terminal() {
			rep.Actions = append(rep.Actions, findingActions(c, f)[0])
			if len(rep.Actions) >= 5 {
				break
			}
		}
	}
	return rep, nil
}

// UpdateFindingStatus triages or dismisses a finding. Dismissals require a
// reason and are indexed for cross-case recall (best effort) — a rejected
// finding is as valuable as a rejected hypothesis.
func (k *Kernel) UpdateFindingStatus(ctx context.Context, in FindingStatusInput) (FindingReport, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return FindingReport{Envelope: errEnvelope(in.TaskID, err.Error())}, nil
	}
	status := domain.FindingStatus(strings.ToLower(strings.TrimSpace(in.Status)))
	if status != domain.FindingTriaged && status != domain.FindingDismissed {
		return FindingReport{Envelope: k.errEnvelopeActions(c.ID, "finding status update must be triaged or dismissed (convert opens a child case)",
			domain.NextAction{Tool: "cortex_finding", Command: cortexCommand(c, "finding", "dismiss", c.ID, in.FindingID, "--reason", "REASON"),
				Reason: "choose a disposition", Arguments: cloneArgs(knownActionArgs(c), "findingId", in.FindingID),
				Inputs: []string{"operation"}, Candidates: map[string][]string{"operation": {"triage", "dismiss", "convert"}}})}, nil
	}
	reason := strings.TrimSpace(in.Reason)
	if status == domain.FindingDismissed && reason == "" {
		return FindingReport{Envelope: k.errEnvelopeActions(c.ID, "dismissing a finding needs a reason",
			domain.NextAction{Tool: "cortex_finding", Command: cortexCommand(c, "finding", "dismiss", c.ID, in.FindingID, "--reason", "REASON"),
				Reason: "say why the finding is not actionable so recall can reuse it", Arguments: cloneArgs(knownActionArgs(c), "findingId", in.FindingID, "operation", "dismiss"),
				Inputs: []string{"reason"}})}, nil
	}
	if textExceeds(reason, maxRecordTextBytes) {
		return FindingReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("finding reason exceeds %d bytes", maxRecordTextBytes))}, nil
	}
	updated, err := k.store.UpdateFinding(c.ID, strings.TrimSpace(in.FindingID), func(f *domain.Finding) error {
		if f.Status.Terminal() {
			return fmt.Errorf("finding %s is already %s", f.ID, f.Status)
		}
		f.Status = status
		f.Reason = k.red.String(reason)
		f.Sensitive = f.Sensitive || k.red.Detected(reason)
		if actor := strings.TrimSpace(in.Actor); actor != "" {
			f.Actor = k.red.String(actor)
		}
		f.UpdatedAt = k.now().UTC()
		return nil
	})
	if err != nil {
		if errors.Is(err, casefs.ErrFindingNotFound) {
			return FindingReport{Envelope: k.errEnvelopeActions(c.ID, err.Error(), domain.NextAction{
				Tool: "cortex_finding", Command: cortexCommand(c, "finding", "list", c.ID),
				Reason: "list the findings recorded in this case", Arguments: cloneArgs(knownActionArgs(c), "operation", "list"),
				Inputs: []string{"findingId"}, Candidates: map[string][]string{"findingId": k.findingIDCandidates(c.ID)}})}, nil
		}
		return FindingReport{Envelope: errEnvelope(c.ID, err.Error())}, nil
	}
	if status == domain.FindingDismissed {
		k.indexDismissedFinding(ctx, c, updated)
	}
	findings, _ := k.store.Findings(c.ID)
	rep := FindingReport{
		Envelope: domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status,
			Summary: fmt.Sprintf("finding %s is now %s", updated.ID, updated.Status), NextActions: nextForPhase(c.Status)},
		Finding: &updated, Counts: countFindings(findings),
	}
	k.attachStructuredActions(&rep.Envelope, c)
	return rep, nil
}

func (k *Kernel) findingIDCandidates(taskID string) []string {
	findings, err := k.store.Findings(taskID)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range findings {
		if !f.Status.Terminal() {
			out = append(out, f.ID)
		}
		if len(out) >= maxRejectionCandidates {
			break
		}
	}
	return out
}

// indexDismissedFinding stores a dismissed finding in cross-case recall. It
// is excluded (not masked) when the redactor fires, like hypotheses.
func (k *Kernel) indexDismissedFinding(ctx context.Context, c *domain.CaseFile, f domain.Finding) {
	if k.recaller == nil || f.Sensitive {
		return
	}
	statement := f.Title
	if f.Detail != "" {
		statement += " — " + f.Detail
	}
	if k.red.Detected(statement) || k.red.Detected(f.Reason) || k.red.Detected(c.Goal) {
		return
	}
	rec := adapters.IndexRecord{
		Key: c.ID + "/" + f.ID, Kind: "finding", TaskID: c.ID, Repo: c.Workspace.Repository,
		Goal: c.Goal, Statement: statement, Status: string(f.Status), Confidence: f.Severity,
		ResolvedReason: f.Reason, Surface: string(f.Kind), Timestamp: f.UpdatedAt,
	}
	if err := k.recaller.IndexCase(ctx, rec); err != nil && !isMissingAdapter(err) {
		k.recordWrite(c.ID, "veclite", "finding_index", err)
	}
}

// ConvertFinding opens (or resumes) the child case for a finding. The child
// is linked to the parent, keyed for retry safety, and — for change work —
// registers the finding as its immutable acceptance criterion so the child
// cannot complete green without proving the finding was addressed.
func (k *Kernel) ConvertFinding(ctx context.Context, in ConvertFindingInput) (FindingReport, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return FindingReport{Envelope: errEnvelope(in.TaskID, err.Error())}, nil
	}
	findings, err := k.store.Findings(c.ID)
	if err != nil {
		return FindingReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	var finding *domain.Finding
	for i := range findings {
		if findings[i].ID == strings.TrimSpace(in.FindingID) {
			finding = &findings[i]
			break
		}
	}
	if finding == nil {
		return FindingReport{Envelope: k.errEnvelopeActions(c.ID, "finding not found: "+in.FindingID, domain.NextAction{
			Tool: "cortex_finding", Command: cortexCommand(c, "finding", "list", c.ID),
			Reason: "list the findings recorded in this case", Arguments: cloneArgs(knownActionArgs(c), "operation", "list"),
			Inputs: []string{"findingId"}, Candidates: map[string][]string{"findingId": k.findingIDCandidates(c.ID)}})}, nil
	}
	if finding.Status == domain.FindingDismissed {
		return FindingReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("finding %s was dismissed: %s", finding.ID, finding.Reason))}, nil
	}
	mode, ok := normalizeMode(domain.Mode(in.Mode))
	if !ok || mode == domain.ModeSurvey {
		return FindingReport{Envelope: k.errEnvelopeActions(c.ID, "conversion mode must be change, investigate, or review",
			domain.NextAction{Tool: "cortex_finding", Command: cortexCommand(c, "finding", "convert", c.ID, finding.ID, "--mode", "MODE"),
				Reason: "choose the child case mode", Arguments: cloneArgs(knownActionArgs(c), "findingId", finding.ID, "operation", "convert"),
				Inputs: []string{"mode"}, Candidates: map[string][]string{"mode": {"change", "investigate", "review"}}})}, nil
	}
	risk := strings.ToLower(strings.TrimSpace(in.Risk))
	if risk == "" {
		risk = finding.Severity
	}
	surfaces := in.Surfaces
	if len(surfaces) == 0 {
		surfaces = append([]domain.Surface(nil), c.Surfaces...)
	}
	goal := finding.Title
	if finding.Kind != domain.FindingBug {
		goal = string(finding.Kind) + ": " + finding.Title
	}
	start := StartInput{
		Goal: goal, Mode: mode, Surfaces: surfaces, Risk: risk,
		Actor: strings.TrimSpace(in.Actor), ParentTaskID: c.ID,
		IdempotencyKey: c.ID + "/" + finding.ID,
	}
	if mode == domain.ModeChange {
		start.AcceptanceCriteria = []domain.AcceptanceCriterion{{ID: finding.CriterionID(), Statement: finding.Title}}
	}
	child, err := k.OpenTask(ctx, OpenInput{StartInput: start})
	if err != nil {
		return FindingReport{Envelope: child}, err
	}
	if !child.OK || child.TaskID == "" {
		return FindingReport{Envelope: child}, nil
	}
	updated, err := k.store.UpdateFinding(c.ID, finding.ID, func(f *domain.Finding) error {
		if f.Status == domain.FindingConverted && f.ConvertedTaskID != child.TaskID {
			return fmt.Errorf("finding %s was already converted into %s", f.ID, f.ConvertedTaskID)
		}
		f.Status = domain.FindingConverted
		f.ConvertedTaskID = child.TaskID
		f.UpdatedAt = k.now().UTC()
		return nil
	})
	if err != nil {
		return FindingReport{Envelope: errEnvelope(c.ID, err.Error())}, nil
	}
	all, _ := k.store.Findings(c.ID)
	rep := FindingReport{
		Envelope: domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status,
			Summary:  fmt.Sprintf("finding %s converted into child case %s (%s)", updated.ID, child.TaskID, child.Phase),
			Warnings: child.Warnings, Actions: child.Actions, NextActions: child.NextActions},
		Finding: &updated, Counts: countFindings(all), ChildTaskID: child.TaskID,
	}
	k.checkpoint(c.ID)
	return rep, nil
}
