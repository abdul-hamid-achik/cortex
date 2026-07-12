package kernel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// caseRecaller is the subset of the veclite adapter the kernel uses for
// cross-case disproof recall (SPEC §15.4). *adapters.Veclite satisfies it;
// tests inject a fake. Every method is best-effort: a missing adapter or a
// failed embed is warned-on, never a hard failure that blocks a phase.
type caseRecaller interface {
	IndexCase(ctx context.Context, rec adapters.IndexRecord) error
	RecallCases(ctx context.Context, query, repo string, limit int) ([]adapters.RecallHit, error)
}

// SetRecaller installs a cross-case recall surface (used by tests to inject a
// fake). Production kernels wire *adapters.Veclite in New.
func (k *Kernel) SetRecaller(r caseRecaller) { k.recaller = r }

// --- indexing (redaction-gated, sensitive-excluded) ---
// ReindexCasesReport summarizes a central-session recall backfill. Session and
// record counts are separate so a load failure is never mistaken for an empty
// case, and every non-indexed record is classified as skipped or failed.
type ReindexCasesReport struct {
	SessionsScanned   int      `json:"sessionsScanned"`
	SessionLoadFailed int      `json:"sessionLoadFailed"`
	RecordsScanned    int      `json:"recordsScanned"`
	Indexed           int      `json:"indexed"`
	Skipped           int      `json:"skipped"`
	Failed            int      `json:"failed"`
	Warnings          []string `json:"warnings"`
}

type recallIndexOutcome uint8

const (
	recallIndexSkipped recallIndexOutcome = iota
	recallIndexIndexed
	recallIndexFailed
)

type recallIndexResult struct {
	outcome recallIndexOutcome
	err     error
}

// indexResolvedHypothesis redacts and indexes one resolved hypothesis
// (rejected/challenged are the gold; confirmed is lower value but still useful).
// Best-effort: a missing veclite or a failed embed is warned-once and never
// blocks the calling phase. A hypothesis whose text trips the redactor is
// SKIPPED entirely (exclusion, not masking) — it is too hot to index cross-repo.
func (k *Kernel) indexResolvedHypothesis(ctx context.Context, c *domain.CaseFile, h domain.Hypothesis, reason string) {
	if k.recaller == nil {
		return
	}
	evidence, err := k.store.Evidence(c.ID)
	if err != nil {
		k.recordWrite(c.ID, "veclite", "case_index", fmt.Errorf("load evidence for recall gate: %w", err))
		return
	}
	if result := k.indexResolvedHypothesisResult(ctx, c, h, reason, evidence); result.err != nil {
		k.recordWrite(c.ID, "veclite", "case_index", result.err)
	}
}

func (k *Kernel) indexResolvedHypothesisResult(ctx context.Context, c *domain.CaseFile, h domain.Hypothesis, reason string, evidence []domain.Evidence) recallIndexResult {
	if k.recaller == nil {
		return recallIndexResult{outcome: recallIndexFailed, err: fmt.Errorf("cross-case recall is unavailable")}
	}
	key := c.ID + "/" + h.ID
	disproof := disproofNote(h.DisproveBy)
	if k.indexFieldsDetected(key, c.ID, c.Workspace.Repository, c.Goal, h.Statement,
		string(h.Status), string(h.Confidence), disproof, reason) ||
		referencesSensitiveEvidence(h.Supports, evidence) {
		return recallIndexResult{outcome: recallIndexSkipped}
	}
	rec := adapters.IndexRecord{
		Key:            k.red.String(key),
		Kind:           k.red.String("hypothesis"),
		TaskID:         k.red.String(c.ID),
		Repo:           k.red.String(c.Workspace.Repository),
		Goal:           k.red.String(c.Goal),
		Statement:      k.red.String(h.Statement),
		Status:         k.red.String(string(h.Status)),
		Confidence:     k.red.String(string(h.Confidence)),
		DisproveBy:     k.red.String(disproof),
		ResolvedReason: k.red.String(reason),
		Timestamp:      k.now(),
	}
	if err := k.recaller.IndexCase(ctx, rec); err != nil {
		return recallIndexResult{outcome: recallIndexFailed, err: err}
	}
	return recallIndexResult{outcome: recallIndexIndexed}
}

// indexReceipt redacts and indexes one definitive (passed/failed) verification
// receipt. Sensitive receipts (VerificationRecord.Sensitive) are skipped
// entirely (SPEC §16.2 #5) — never archived into the cross-repo store.
func (k *Kernel) indexReceipt(ctx context.Context, c *domain.CaseFile, r domain.VerificationRecord) {
	if k.recaller == nil || r.Sensitive {
		return
	}
	evidence, err := k.store.Evidence(c.ID)
	if err != nil {
		k.recordWrite(c.ID, "veclite", "case_index", fmt.Errorf("load evidence for recall gate: %w", err))
		return
	}
	if result := k.indexReceiptResult(ctx, c, r, evidence); result.err != nil {
		k.recordWrite(c.ID, "veclite", "case_index", result.err)
	}
}

func (k *Kernel) indexReceiptResult(ctx context.Context, c *domain.CaseFile, r domain.VerificationRecord, evidence []domain.Evidence) recallIndexResult {
	if k.recaller == nil {
		return recallIndexResult{outcome: recallIndexFailed, err: fmt.Errorf("cross-case recall is unavailable")}
	}
	if r.Sensitive || referencesSensitiveEvidence(r.Evidence, evidence) {
		return recallIndexResult{outcome: recallIndexSkipped}
	}
	key := c.ID + "/" + r.ID
	if k.indexFieldsDetected(key, c.ID, c.Workspace.Repository, c.Goal, r.Claim,
		string(r.Status), string(r.Surface), r.Notes, r.Artifact) {
		return recallIndexResult{outcome: recallIndexSkipped}
	}
	rec := adapters.IndexRecord{
		Key:            k.red.String(key),
		Kind:           k.red.String("verification"),
		TaskID:         k.red.String(c.ID),
		Repo:           k.red.String(c.Workspace.Repository),
		Goal:           k.red.String(c.Goal),
		Statement:      k.red.String(r.Claim),
		Status:         k.red.String(string(r.Status)),
		Confidence:     k.red.String("medium"),
		ResolvedReason: k.red.String(r.Notes),
		Surface:        k.red.String(string(r.Surface)),
		Artifact:       k.red.String(r.Artifact),
		Timestamp:      r.Timestamp,
	}
	if err := k.recaller.IndexCase(ctx, rec); err != nil {
		return recallIndexResult{outcome: recallIndexFailed, err: err}
	}
	return recallIndexResult{outcome: recallIndexIndexed}
}

// indexCaseForRecall is the completion-time sweep: index all resolved
// (non-active) hypotheses and definitive (passed/failed) receipts. Resolution
// evidence reconstructs each reason so an upsert cannot erase the richer record
// written by the resolve hook.
// Called from Remember after the case is durably saved.
func (k *Kernel) indexCaseForRecall(ctx context.Context, c *domain.CaseFile, hyps []domain.Hypothesis, receipts []domain.VerificationRecord) {
	if k.recaller == nil {
		return
	}
	evidence, err := k.store.Evidence(c.ID)
	if err != nil {
		k.recordWrite(c.ID, "veclite", "case_index", fmt.Errorf("load evidence for recall gate: %w", err))
		return
	}
	for _, h := range hyps {
		if h.Status != domain.HypActive {
			if result := k.indexResolvedHypothesisResult(ctx, c, h, resolvedReasonFromEvidence(h.ID, evidence), evidence); result.err != nil {
				k.recordWrite(c.ID, "veclite", "case_index", result.err)
			}
		}
	}
	for _, r := range receipts {
		if r.Definitive() {
			if result := k.indexReceiptResult(ctx, c, r, evidence); result.err != nil {
				k.recordWrite(c.ID, "veclite", "case_index", result.err)
			}
		}
	}
}

// ReindexCases rebuilds the recall index from active central sessions only.
// Strict directory enumeration counts corrupt case.json files that the regular
// session list intentionally hides from human audit views.
func (k *Kernel) ReindexCases(ctx context.Context) (ReindexCasesReport, error) {
	report := ReindexCasesReport{Warnings: []string{}}
	sessions, err := centralSessionCandidates()
	if err != nil {
		return report, fmt.Errorf("enumerate central sessions: %w", err)
	}
	for _, session := range sessions {
		report.SessionsScanned++
		sessionStore, err := casefs.New(filepath.Join(config.SessionsRoot(), session.slug))
		if err != nil {
			report.SessionLoadFailed++
			report.Warnings = append(report.Warnings, "one central session store was unreadable and was skipped")
			continue
		}
		caseFile, err := sessionStore.Load(session.id)
		if err != nil {
			report.SessionLoadFailed++
			report.Warnings = append(report.Warnings, "one central session had an unreadable case.json and was skipped")
			continue
		}
		caseCfg := config.For(caseFile.Workspace.Root)
		indexer := *k
		indexer.red = redact.New(caseCfg.RedactLiterals...)
		detail, err := LoadSession(session.slug, session.id)
		if err != nil {
			report.SessionLoadFailed++
			warning := fmt.Sprintf("load %s/%s: %v", caseFile.Workspace.Repository, caseFile.ID, err)
			report.Warnings = append(report.Warnings, indexer.red.String(warning))
			continue
		}
		records, indexed, skipped, failed, warnings := indexer.indexCaseForRecallResults(ctx, detail.Case, detail.Evidence, detail.Hyps, detail.Receipts)
		report.RecordsScanned += records
		report.Indexed += indexed
		report.Skipped += skipped
		report.Failed += failed
		report.Warnings = append(report.Warnings, warnings...)
	}
	return report, nil
}

type centralSessionCandidate struct {
	slug string
	id   string
}

func centralSessionCandidates() ([]centralSessionCandidate, error) {
	root := config.SessionsRoot()
	slugs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var candidates []centralSessionCandidate
	for _, slug := range slugs {
		if !slug.IsDir() {
			continue
		}
		tasks, err := os.ReadDir(filepath.Join(root, slug.Name()))
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			if task.IsDir() {
				candidates = append(candidates, centralSessionCandidate{slug: slug.Name(), id: task.Name()})
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].slug == candidates[j].slug {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].slug < candidates[j].slug
	})
	return candidates, nil
}

func (k *Kernel) indexCaseForRecallResults(ctx context.Context, c *domain.CaseFile, evidence []domain.Evidence, hyps []domain.Hypothesis, receipts []domain.VerificationRecord) (records, indexed, skipped, failed int, warnings []string) {
	record := func(kind, id string, result recallIndexResult) {
		switch result.outcome {
		case recallIndexIndexed:
			indexed++
		case recallIndexSkipped:
			skipped++
		case recallIndexFailed:
			failed++
			warning := fmt.Sprintf("index %s/%s %s %s: %v", c.Workspace.Repository, c.ID, kind, id, result.err)
			warnings = append(warnings, k.red.String(warning))
		}
	}
	for _, h := range hyps {
		records++
		if h.Status == domain.HypActive {
			skipped++
			continue
		}
		record("hypothesis", h.ID, k.indexResolvedHypothesisResult(ctx, c, h, resolvedReasonFromEvidence(h.ID, evidence), evidence))
	}
	for _, r := range receipts {
		records++
		if r.Status != domain.VerifyPassed && r.Status != domain.VerifyFailed {
			skipped++
			continue
		}
		record("verification", r.ID, k.indexReceiptResult(ctx, c, r, evidence))
	}
	return records, indexed, skipped, failed, warnings
}

func resolvedReasonFromEvidence(hypothesisID string, evidence []domain.Evidence) string {
	prefix := "hypothesis " + hypothesisID + " "
	for i := len(evidence) - 1; i >= 0; i-- {
		claim := evidence[i].Claim
		if !strings.HasPrefix(claim, prefix) {
			continue
		}
		separator := strings.Index(claim, ": ")
		if separator < 0 {
			continue
		}
		reason := claim[separator+2:]
		if evidenceSuffix := strings.Index(reason, " [evidence:"); evidenceSuffix >= 0 {
			reason = reason[:evidenceSuffix]
		}
		return strings.TrimSpace(reason)
	}
	return ""
}

func (k *Kernel) indexFieldsDetected(fields ...string) bool {
	for _, field := range fields {
		if k.red.Detected(field) || strings.Contains(field, "«redacted»") {
			return true
		}
	}
	return false
}

func referencesSensitiveEvidence(ids []string, evidence []domain.Evidence) bool {
	for _, id := range ids {
		for _, item := range evidence {
			if item.ID == id && item.Sensitivity == domain.SensitivitySensitive {
				return true
			}
		}
	}
	return false
}

// disproofNote flattens a Disproof into a single string for the payload.
func disproofNote(d domain.Disproof) string {
	parts := []string{d.Kind, d.Tool, d.Contract, d.Note}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " | ")
}

// --- recall (orient + investigate + MCP) ---

// recallPriorCases recalls prior related cases for a query, scoped to the
// repo first then unscoped (the cross-repo tier), stamp each hit as
// model_inference/low orientation evidence, and dedupe by task id. Returns the
// stamped evidence, a single warning (empty on success / when veclite is
// simply absent — absent is warn-once, not per-call), and the count for a
// NextActions nudge.
func (k *Kernel) recallPriorCases(ctx context.Context, c *domain.CaseFile, query string, limit int) ([]domain.Evidence, string, int) {
	if k.recaller == nil || strings.TrimSpace(query) == "" {
		return nil, "", 0
	}
	seen := map[string]bool{}
	var facts []domain.Evidence
	add := func(hits []adapters.RecallHit) {
		for _, h := range hits {
			tid := payloadStr(h.Payload, "task_id")
			if tid != "" && seen[tid] {
				continue
			}
			if tid != "" {
				seen[tid] = true
			}
			claim := adapters.RecallClaim(h.Payload)
			stableID := "ev_orientation_recall_" + strings.TrimPrefix(claimID(domain.SurfaceCode, tid+"\x00"+claim), "claim_")
			if ev, err := k.stampEvidenceOnce(c.ID, stableID, "veclite", adapters.Fact{
				Kind: "model_inference", Confidence: "low", Claim: claim,
			}, c.CreatedAt); err == nil {
				facts = append(facts, ev)
			}
		}
	}
	// Tier 1: repo-scoped (this project's prior disproofs are the strongest signal).
	if hits, err := k.recaller.RecallCases(ctx, query, c.Workspace.Repository, limit); err == nil {
		add(hits)
	} else if !isMissingAdapter(err) {
		return nil, fmt.Sprintf("cross-case recall failed: %s", err), 0
	}
	// Tier 2: cross-repo (unscoped) — prior disproofs from other projects.
	if hits, err := k.recaller.RecallCases(ctx, query, "", limit); err == nil {
		add(hits)
	}
	return facts, "", len(facts)
}

// RecallCasesEnvelope is the MCP/CLI recall surface: run a recall (optionally
// repo-scoped) and return the hits as model_inference/low facts in an envelope.
func (k *Kernel) RecallCasesEnvelope(ctx context.Context, query, repo string, limit int) (domain.Envelope, error) {
	if k.recaller == nil {
		return domain.Envelope{OK: true, Summary: "cross-case recall unavailable (veclite not configured)", Facts: nil}, nil
	}
	if limit < 1 {
		limit = 5
	}
	hits, err := k.recaller.RecallCases(ctx, query, repo, limit)
	if err != nil {
		if isMissingAdapter(err) {
			return domain.Envelope{OK: true, Summary: "cross-case recall unavailable (veclite not on PATH)"}, nil
		}
		return domain.Envelope{OK: false, Summary: "cross-case recall failed: " + err.Error(), Error: err.Error()}, nil
	}
	var facts []domain.FactView
	for _, h := range hits {
		facts = append(facts, domain.FactView{
			Claim:      adapters.RecallClaim(h.Payload),
			Confidence: domain.ConfidenceLow,
			Source:     "veclite",
			Kind:       domain.KindModelInference,
		})
	}
	summary := fmt.Sprintf("recalled %d prior case(s) for %q", len(hits), clipStr(query, 60))
	return domain.Envelope{OK: true, Summary: summary, Facts: facts, RawAvailable: false}, nil
}

// isMissingAdapter reports whether err is the "binary missing / disabled" signal
// (best-effort absence, not a failure to surface as a warning).
func isMissingAdapter(err error) bool {
	return err == adapters.ErrToolMissing
}

// payloadStr reads a string field from a veclite payload map.
func payloadStr(p map[string]interface{}, key string) string {
	if s, ok := p[key].(string); ok {
		return s
	}
	return ""
}
