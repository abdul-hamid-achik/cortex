package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
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

// indexResolvedHypothesis redacts and indexes one resolved hypothesis
// (rejected/challenged are the gold; confirmed is lower value but still useful).
// Best-effort: a missing veclite or a failed embed is warned-once and never
// blocks the calling phase. A hypothesis whose text trips the redactor is
// SKIPPED entirely (exclusion, not masking) — it is too hot to index cross-repo.
func (k *Kernel) indexResolvedHypothesis(ctx context.Context, c *domain.CaseFile, h domain.Hypothesis, reason string) {
	if k.recaller == nil {
		return
	}
	stmt := k.red.String(h.Statement)
	reason = k.red.String(reason)
	goal := k.red.String(c.Goal)
	disproof := disproofNote(h.DisproveBy)
	// Redaction gate (SPEC §16.3): a hypothesis that triggered a redactor match
	// is excluded from the cross-repo index, not masked into it.
	if k.red.Detected(h.Statement) || k.red.Detected(reason) || k.red.Detected(disproof) {
		return
	}
	rec := adapters.IndexRecord{
		Key:            c.ID + "/" + h.ID,
		Kind:           "hypothesis",
		TaskID:         c.ID,
		Repo:           c.Workspace.Repository,
		Goal:           goal,
		Statement:      stmt,
		Status:         string(h.Status),
		Confidence:     string(h.Confidence),
		DisproveBy:     disproof,
		ResolvedReason: reason,
		Timestamp:      k.now(),
	}
	if err := k.recaller.IndexCase(ctx, rec); err != nil {
		k.recordWrite(c.ID, "veclite", "case_index", err)
	}
}

// indexReceipt redacts and indexes one definitive (passed/failed) verification
// receipt. Sensitive receipts (VerificationRecord.Sensitive) are skipped
// entirely (SPEC §16.2 #5) — never archived into the cross-repo store.
func (k *Kernel) indexReceipt(ctx context.Context, c *domain.CaseFile, r domain.VerificationRecord) {
	if k.recaller == nil || r.Sensitive {
		return
	}
	claim := k.red.String(r.Claim)
	goal := k.red.String(c.Goal)
	if k.red.Detected(r.Claim) || k.red.Detected(r.Notes) {
		return
	}
	rec := adapters.IndexRecord{
		Key:            c.ID + "/" + r.ID,
		Kind:           "verification",
		TaskID:         c.ID,
		Repo:           c.Workspace.Repository,
		Goal:           goal,
		Statement:      claim,
		Status:         string(r.Status),
		Confidence:     "medium",
		ResolvedReason: k.red.String(r.Notes),
		Surface:        string(r.Surface),
		Artifact:       r.Artifact,
		Timestamp:      r.Timestamp,
	}
	if err := k.recaller.IndexCase(ctx, rec); err != nil {
		k.recordWrite(c.ID, "veclite", "case_index", err)
	}
}

// indexCaseForRecall is the completion-time sweep: index all resolved
// (non-active) hypotheses and definitive (passed/failed) receipts. The
// just-resolved hypothesis was already indexed by the resolve hook with its
// reason; this backfills the rest (reason empty — the ledger is authoritative).
// Called from Remember after the case is durably saved.
func (k *Kernel) indexCaseForRecall(ctx context.Context, c *domain.CaseFile, hyps []domain.Hypothesis, receipts []domain.VerificationRecord) {
	if k.recaller == nil {
		return
	}
	for _, h := range hyps {
		if h.Status != domain.HypActive {
			k.indexResolvedHypothesis(ctx, c, h, "")
		}
	}
	for _, r := range receipts {
		if r.Status == domain.VerifyPassed || r.Status == domain.VerifyFailed {
			k.indexReceipt(ctx, c, r)
		}
	}
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
			if ev, err := k.stampEvidence(c.ID, "veclite", adapters.Fact{
				Kind: "model_inference", Confidence: "low", Claim: claim,
			}); err == nil {
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
