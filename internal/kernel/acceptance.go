package kernel

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

const (
	maxStatusClaimProofs        = domain.MaxAcceptanceCriteria
	maxClaimProofEvidenceRefs   = 2
	maxClaimProofStatementBytes = 160
	maxClaimProofRefBytes       = 160
	maxClaimProofMetadataBytes  = 128
)

// ClaimProof is the bounded, model-facing projection of the current named
// claim receipt for one registered criterion (or, for legacy cases, one stable
// non-empty claim ID). Evidence includes only non-sensitive durable refs.
type ClaimProof struct {
	ClaimID              string                     `json:"claimId"`
	ClaimIDDigest        string                     `json:"claimIdDigest,omitempty"`
	ClaimIDTruncated     bool                       `json:"claimIdTruncated,omitempty"`
	Statement            string                     `json:"statement"`
	StatementDigest      string                     `json:"statementDigest"`
	StatementTruncated   bool                       `json:"statementTruncated,omitempty"`
	Registered           bool                       `json:"registered,omitempty"`
	ReceiptID            string                     `json:"receiptId,omitempty"`
	BatchID              string                     `json:"batchId,omitempty"`
	Status               domain.VerificationStatus  `json:"status"`
	Binding              domain.VerificationBinding `json:"binding,omitempty"`
	Evidence             []string                   `json:"evidence,omitempty"`
	Revision             string                     `json:"revision,omitempty"`
	DirtyDigest          string                     `json:"dirtyDigest,omitempty"`
	SensitiveRefsOmitted bool                       `json:"sensitiveRefsOmitted,omitempty"`
	EvidenceRefsOmitted  int                        `json:"evidenceRefsOmitted,omitempty"`
	MetadataTruncated    bool                       `json:"metadataTruncated,omitempty"`
}

func (k *Kernel) normalizeAcceptanceCriteria(input []domain.AcceptanceCriterion) ([]domain.AcceptanceCriterion, error) {
	if len(input) == 0 {
		return nil, nil
	}
	criteria := make([]domain.AcceptanceCriterion, 0, len(input))
	for _, raw := range input {
		criterion := domain.AcceptanceCriterion{
			ID:        strings.TrimSpace(raw.ID),
			Statement: strings.TrimSpace(raw.Statement),
		}
		if err := criterion.Validate(); err != nil {
			return nil, err
		}
		if !validClaimIdentifier(criterion.ID) {
			return nil, fmt.Errorf("acceptance criterion id %q must contain only letters, digits, dash, or underscore", criterion.ID)
		}
		if k.red.Detected(criterion.ID) {
			return nil, errors.New("acceptance criterion id must be a stable non-sensitive identifier")
		}
		criterion.Statement = k.red.String(criterion.Statement)
		if err := criterion.Validate(); err != nil {
			return nil, err
		}
		criteria = append(criteria, criterion)
	}
	if err := domain.ValidateAcceptanceCriteria(criteria); err != nil {
		return nil, err
	}
	// Criterion order is presentation detail, not task identity. Canonicalizing
	// by ID keeps a retry safe when a model serializes the same contract in a
	// different array order.
	slices.SortFunc(criteria, func(a, b domain.AcceptanceCriterion) int {
		return strings.Compare(a.ID, b.ID)
	})
	return criteria, nil
}

func acceptanceCriteriaEqual(a, b []domain.AcceptanceCriterion) bool {
	return slices.Equal(a, b)
}

func validateRegisteredClaimIdentities(criteria []domain.AcceptanceCriterion, claims []domain.VerificationClaim) error {
	if len(criteria) == 0 || len(claims) == 0 {
		return nil
	}
	registered := make(map[string]string, len(criteria))
	for _, criterion := range criteria {
		registered[criterion.ID] = criterion.Statement
	}
	for _, claim := range claims {
		statement, ok := registered[claim.ID]
		if ok && claim.Statement != statement {
			return fmt.Errorf("verification claim id %q is registered for a different acceptance statement", claim.ID)
		}
	}
	return nil
}

// assessCaseVerification adds the immutable acceptance contract to the shared
// verifier/named-claim assessment. Legacy cases with no registered criteria
// retain the exact historical behavior of assessVerification.
func assessCaseVerification(c *domain.CaseFile, receipts []domain.VerificationRecord) VerificationAssessment {
	if c == nil {
		return assessVerification(nil, receipts)
	}
	assessment := assessVerification(c.VerificationRequired, receipts)
	if len(c.AcceptanceCriteria) == 0 {
		return assessment
	}

	current := currentVerificationReceipts(receipts)
	named := currentNamedClaims(receipts, current)
	byID := make(map[string]domain.VerificationRecord, len(named))
	for _, receipt := range named {
		if receipt.ClaimID != "" {
			byID[receipt.ClaimID] = receipt
		}
	}
	for _, criterion := range c.AcceptanceCriteria {
		receipt, ok := byID[criterion.ID]
		if ok && receipt.Claim == criterion.Statement && receipt.Proven() {
			assessment.SatisfiedCriteria = append(assessment.SatisfiedCriteria, criterion.ID)
			continue
		}
		assessment.MissingCriteria = append(assessment.MissingCriteria, criterion.ID)
		if !ok || receipt.Claim != criterion.Statement || !receipt.Failed() {
			assessment.NonPassingClaims = append(assessment.NonPassingClaims, criterion.Statement)
		}
	}
	assessment.SatisfiedCriteria = dedupeSorted(assessment.SatisfiedCriteria)
	assessment.MissingCriteria = dedupeSorted(assessment.MissingCriteria)
	assessment.NonPassingClaims = dedupeSorted(assessment.NonPassingClaims)
	if len(assessment.MissingCriteria) > 0 && assessment.Outcome == VerificationVerified {
		assessment.Outcome = VerificationPartial
	}
	return assessment
}

func claimProofsForCase(taskID string, c *domain.CaseFile, receipts []domain.VerificationRecord) ([]ClaimProof, int) {
	if c == nil {
		return nil, 0
	}
	current := currentVerificationReceipts(receipts)
	named := currentNamedClaims(receipts, current)
	byID := make(map[string]domain.VerificationRecord, len(named))
	for _, receipt := range named {
		if receipt.ClaimID != "" {
			byID[receipt.ClaimID] = receipt
		}
	}

	proofs := make([]ClaimProof, 0, len(c.AcceptanceCriteria))
	if len(c.AcceptanceCriteria) > 0 {
		for _, criterion := range c.AcceptanceCriteria {
			receipt, ok := byID[criterion.ID]
			if !ok || receipt.Claim != criterion.Statement {
				proofs = append(proofs, missingClaimProof(criterion))
				continue
			}
			proofs = append(proofs, claimProofFromReceipt(taskID, receipt, receipts, true))
		}
	} else {
		for _, receipt := range named {
			if receipt.ClaimID != "" {
				proofs = append(proofs, claimProofFromReceipt(taskID, receipt, receipts, false))
			}
		}
	}

	total := len(proofs)
	if len(proofs) > maxStatusClaimProofs {
		proofs = append([]ClaimProof(nil), proofs[len(proofs)-maxStatusClaimProofs:]...)
	}
	return proofs, total
}

func claimProofFromReceipt(taskID string, receipt domain.VerificationRecord, receipts []domain.VerificationRecord, registered bool) ClaimProof {
	claimID, claimIDTruncated := compactClaimProofText(receipt.ClaimID, domain.MaxAcceptanceCriterionIDBytes)
	statement, statementTruncated := compactClaimProofText(receipt.Claim, maxClaimProofStatementBytes)
	receiptID, receiptIDTruncated := compactClaimProofText(receipt.ID, maxClaimProofMetadataBytes)
	batchID, batchIDTruncated := compactClaimProofText(receipt.BatchID, maxClaimProofMetadataBytes)
	revision, revisionTruncated := compactClaimProofText(receipt.Revision, maxClaimProofMetadataBytes)
	digest, digestTruncated := compactClaimProofText(receipt.DirtyDigest, maxClaimProofMetadataBytes)
	proof := ClaimProof{
		ClaimID: claimID, Statement: statement, StatementDigest: claimStatementDigest(receipt.Claim),
		StatementTruncated: statementTruncated, Registered: registered,
		ReceiptID: receiptID, BatchID: batchID, Status: receipt.Status,
		Binding: receipt.Binding, Revision: revision, DirtyDigest: digest,
		ClaimIDTruncated:  claimIDTruncated,
		MetadataTruncated: receiptIDTruncated || batchIDTruncated || revisionTruncated || digestTruncated,
	}
	if claimIDTruncated {
		proof.ClaimIDDigest = claimStatementDigest(receipt.ClaimID)
	}
	batch := []domain.VerificationRecord{receipt}
	if receipt.BatchID != "" {
		for _, candidate := range receipts {
			if candidate.BatchID == receipt.BatchID && candidate.EffectivePurpose() == domain.VerificationPurposeVerifierRun {
				batch = append(batch, candidate)
			}
		}
	}
	seen := make(map[string]struct{})
	appendRef := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		if len(proof.Evidence) >= maxClaimProofEvidenceRefs {
			proof.EvidenceRefsOmitted++
			return
		}
		if len(ref) > maxClaimProofRefBytes {
			proof.EvidenceRefsOmitted++
			return
		}
		proof.Evidence = append(proof.Evidence, ref)
	}
	for _, candidate := range batch {
		if candidate.Sensitive {
			proof.SensitiveRefsOmitted = true
			continue
		}
		if candidate.ID != "" {
			appendRef("case://" + taskID + "/verification/" + candidate.ID)
		}
		for _, evidenceID := range candidate.Evidence {
			appendRef(evidenceID)
		}
		appendRef(candidate.Artifact)
	}
	return proof
}

func missingClaimProof(criterion domain.AcceptanceCriterion) ClaimProof {
	statement, truncated := compactClaimProofText(criterion.Statement, maxClaimProofStatementBytes)
	return ClaimProof{
		ClaimID: criterion.ID, Statement: statement, StatementDigest: claimStatementDigest(criterion.Statement),
		StatementTruncated: truncated, Registered: true, Status: domain.VerifyNotRun,
	}
}

func claimStatementDigest(statement string) string {
	sum := sha256.Sum256([]byte(statement))
	return fmt.Sprintf("sha256:%x", sum[:])
}

func compactClaimProofText(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	bounded, _ := boundedUTF8(value, limit)
	return bounded, true
}
