package kernel

import (
	"fmt"
	"sort"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// VerificationOutcome is the canonical task-level interpretation of the
// current verification receipts. Every surface (remember, status, metrics,
// sessions/overview, and review) uses this assessment so "verified" cannot mean
// different things in different views.
type VerificationOutcome string

const (
	VerificationVerified   VerificationOutcome = "verified"
	VerificationPartial    VerificationOutcome = "partial"
	VerificationFailed     VerificationOutcome = "failed"
	VerificationUnverified VerificationOutcome = "unverified"
)

// VerificationAssessment explains the canonical outcome. MissingRequired names
// planned verifiers without a current passing verifier-run receipt;
// NonPassingClaims and FailedClaims name current user-facing claim receipts that
// did not pass. SatisfiedRequired is retained for compact session counts.
type VerificationAssessment struct {
	Outcome           VerificationOutcome `json:"outcome"`
	SatisfiedRequired []string            `json:"satisfiedRequired,omitempty"`
	MissingRequired   []string            `json:"missingRequired,omitempty"`
	NonPassingClaims  []string            `json:"nonPassingClaims,omitempty"`
	FailedClaims      []string            `json:"failedClaims,omitempty"`
}

// assessVerification evaluates the latest receipt for each verifier run and
// named claim in the latest revision/diff represented by the input. A failed
// current receipt is failed; an unmet required verifier or non-passing named
// claim is partial when some proof passed and otherwise unverified; only an
// affirmative pass with every requirement and named claim satisfied is verified.
func assessVerification(required []string, receipts []domain.VerificationRecord) VerificationAssessment {
	current := currentVerificationReceipts(receipts)
	verifierRuns := latestReceipts(current, domain.VerificationPurposeVerifierRun)
	namedClaims := currentNamedClaims(receipts, current)

	assessment := VerificationAssessment{}
	hasPass := false
	hasFailedRun := false
	for _, receipt := range verifierRuns {
		switch receipt.Status {
		case domain.VerifyPassed:
			if receipt.Proven() {
				hasPass = true
			}
		case domain.VerifyFailed:
			if receipt.Failed() {
				hasFailedRun = true
			}
		}
	}
	for _, receipt := range namedClaims {
		switch receipt.Status {
		case domain.VerifyPassed:
			if receipt.Proven() {
				hasPass = true
			} else {
				assessment.NonPassingClaims = append(assessment.NonPassingClaims, receipt.Claim)
			}
		case domain.VerifyFailed:
			if receipt.Failed() {
				assessment.FailedClaims = append(assessment.FailedClaims, receipt.Claim)
			} else {
				assessment.NonPassingClaims = append(assessment.NonPassingClaims, receipt.Claim)
			}
		default:
			assessment.NonPassingClaims = append(assessment.NonPassingClaims, receipt.Claim)
		}
	}

	for _, requirement := range required {
		if requirementSatisfied(requirement, verifierRuns) {
			assessment.SatisfiedRequired = append(assessment.SatisfiedRequired, requirement)
		} else {
			assessment.MissingRequired = append(assessment.MissingRequired, requirement)
		}
	}

	assessment.SatisfiedRequired = dedupeSorted(assessment.SatisfiedRequired)
	assessment.MissingRequired = dedupeSorted(assessment.MissingRequired)
	assessment.NonPassingClaims = dedupeSorted(assessment.NonPassingClaims)
	assessment.FailedClaims = dedupeSorted(assessment.FailedClaims)

	switch {
	case hasFailedRun || len(assessment.FailedClaims) > 0:
		assessment.Outcome = VerificationFailed
	case len(assessment.MissingRequired) > 0 || len(assessment.NonPassingClaims) > 0:
		if hasPass {
			assessment.Outcome = VerificationPartial
		} else {
			assessment.Outcome = VerificationUnverified
		}
	case hasPass:
		assessment.Outcome = VerificationVerified
	default:
		assessment.Outcome = VerificationUnverified
	}
	return assessment
}

// currentNamedClaims carries required named claims forward across verifier
// batches that observed the exact same HEAD and dirty tree. Verify callers are
// allowed to rerun only a subset of claims, but omission is not revocation: a
// previously failed/not-run required claim must remain visible until the same
// stable ClaimID is rerun or the workspace state changes. Legacy/unbound records
// without a complete state key retain the historical latest-batch behavior.
func currentNamedClaims(receipts, currentBatch []domain.VerificationRecord) []domain.VerificationRecord {
	if len(currentBatch) == 0 {
		return nil
	}
	revision := currentBatch[len(currentBatch)-1].Revision
	digest := currentBatch[len(currentBatch)-1].DirtyDigest
	if revision == "" || digest == "" {
		return latestReceipts(currentBatch, domain.VerificationPurposeNamedClaim)
	}
	candidates := make([]domain.VerificationRecord, 0, len(receipts))
	for _, receipt := range receipts {
		if receipt.EffectivePurpose() == domain.VerificationPurposeNamedClaim &&
			receipt.Revision == revision && receipt.DirtyDigest == digest {
			candidates = append(candidates, receipt)
		}
	}
	return latestReceipts(candidates, domain.VerificationPurposeNamedClaim)
}

// currentVerificationReceipts selects the latest revision/diff represented in
// the ledger. Legacy receipts have no dirty digest; when all records are legacy,
// they are all retained. New receipts always share the captured revision/digest
// for one verify call, so older workspace states cannot poison the current one.
func currentVerificationReceipts(receipts []domain.VerificationRecord) []domain.VerificationRecord {
	latestBatch := ""
	for i := len(receipts) - 1; i >= 0; i-- {
		if receipts[i].BatchID != "" {
			latestBatch = receipts[i].BatchID
			break
		}
	}
	if latestBatch != "" {
		out := make([]domain.VerificationRecord, 0, len(receipts))
		for _, receipt := range receipts {
			if receipt.BatchID == latestBatch {
				out = append(out, receipt)
			}
		}
		return out
	}
	revision, digest := "", ""
	for i := len(receipts) - 1; i >= 0; i-- {
		if receipts[i].DirtyDigest != "" {
			revision, digest = receipts[i].Revision, receipts[i].DirtyDigest
			break
		}
	}
	if digest == "" {
		return receipts
	}
	out := make([]domain.VerificationRecord, 0, len(receipts))
	for _, receipt := range receipts {
		if receipt.Revision == revision && receipt.DirtyDigest == digest {
			out = append(out, receipt)
		}
	}
	return out
}

// latestReceipts returns the last appended receipt for each logical verifier
// run or named claim. Append order, not wall-clock time, is authoritative for
// legacy records whose timestamp may be absent.
func latestReceipts(receipts []domain.VerificationRecord, purpose domain.VerificationPurpose) []domain.VerificationRecord {
	latest := map[string]domain.VerificationRecord{}
	order := map[string]int{}
	for i, receipt := range receipts {
		if receipt.EffectivePurpose() != purpose {
			continue
		}
		key := receiptKey(receipt, purpose)
		latest[key] = receipt
		order[key] = i
	}
	keys := make([]string, 0, len(latest))
	for key := range latest {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return order[keys[i]] < order[keys[j]] })
	out := make([]domain.VerificationRecord, 0, len(keys))
	for _, key := range keys {
		out = append(out, latest[key])
	}
	return out
}

func receiptKey(receipt domain.VerificationRecord, purpose domain.VerificationPurpose) string {
	if purpose == domain.VerificationPurposeNamedClaim {
		if receipt.ClaimID != "" {
			return receipt.ClaimID
		}
		return fmt.Sprintf("%s\x00%s", receipt.Surface, receipt.Claim)
	}
	return fmt.Sprintf("%s\x00%s\x00%s", receipt.Surface, receipt.Tool, receipt.Claim)
}

// requirementSatisfied is deliberately stricter than the old any-pass rule:
// every current verifier-run receipt matching the required surface must pass.
// This prevents a failed covering spec from being masked by a different pass.
func requirementSatisfied(required string, verifierRuns []domain.VerificationRecord) bool {
	surface := requiredSurface(required)
	matched := false
	for _, receipt := range verifierRuns {
		if receipt.Requirement != "" {
			if receipt.Requirement != required {
				continue
			}
			matched = true
			if !receipt.Proven() {
				return false
			}
			continue
		}
		if receipt.Surface != surface && string(receipt.Surface) != required && receipt.Tool != required {
			continue
		}
		matched = true
		if !receipt.Proven() {
			return false
		}
	}
	return matched
}

func dedupeSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
