package domain

import (
	"strings"
	"time"
)

// VerificationStatus is the outcome of a verification attempt.
// not_run must never be rendered as passed in a final summary.
type VerificationStatus string

const (
	VerifyPassed        VerificationStatus = "passed"
	VerifyFailed        VerificationStatus = "failed"
	VerifyInconclusive  VerificationStatus = "inconclusive"
	VerifyBlocked       VerificationStatus = "blocked"
	VerifyNotApplicable VerificationStatus = "not_applicable"
	VerifyNotRun        VerificationStatus = "not_run"
)

// VerificationClaim is a typed user-facing assertion. Surface is explicit so
// words such as "login", "build", or "TUI" cannot heuristically route a claim
// to the wrong verifier. Kernel write paths require Contract to name the exact
// spec/check; it remains omittable in this domain type only for legacy reads.
type VerificationClaim struct {
	ID        string  `json:"id"`
	Statement string  `json:"statement"`
	Surface   Surface `json:"surface"`
	Verifier  string  `json:"verifier,omitempty"`
	Contract  string  `json:"contract,omitempty"`
	Required  bool    `json:"required"`
}

// Validate rejects claims whose declared verifier cannot prove their surface.
// Code claims may opt into a repository-configured command verifier; all other
// surfaces use their single policy verifier.
func (c VerificationClaim) Validate() error {
	if strings.TrimSpace(c.Statement) == "" {
		return errValidation("verification claim has no statement")
	}
	if !c.Surface.Valid() {
		return errValidation("verification claim has invalid surface")
	}
	verifier := strings.TrimSpace(c.Verifier)
	if verifier == "" {
		return nil
	}
	if verifier == SurfaceVerifier(c.Surface) {
		return nil
	}
	if c.Surface == SurfaceCode && strings.HasPrefix(verifier, "command:") && strings.TrimPrefix(verifier, "command:") != "" {
		return nil
	}
	return errValidation("verification claim verifier does not match its surface")
}

// VerificationPurpose distinguishes a verifier execution receipt from the
// claim-level receipt that maps a user-facing claim onto that execution. Older
// case files predate this field; EffectivePurpose classifies those records from
// their stable shape so existing JSON remains readable and assessable.
type VerificationPurpose string

const (
	VerificationPurposeVerifierRun VerificationPurpose = "verifier_run"
	VerificationPurposeNamedClaim  VerificationPurpose = "named_claim"
)

// VerificationBinding states whether a verifier batch was proven to observe
// one stable workspace revision/diff from start through completion. Empty is a
// legacy receipt and retains historical behavior; new writes always set it.
type VerificationBinding string

const (
	VerificationBound   VerificationBinding = "bound"
	VerificationUnbound VerificationBinding = "unbound"
)

// VerificationRecord is a structured proof record for a specific claim. Every
// record names the exact claim it supports.
type VerificationRecord struct {
	ID      string  `json:"id"`
	BatchID string  `json:"batchId,omitempty"`
	Claim   string  `json:"claim"`
	Surface Surface `json:"surface"`
	// Purpose is omitted in legacy records. New writes always set it; readers use
	// EffectivePurpose so the schema addition remains backward compatible.
	Purpose VerificationPurpose `json:"purpose,omitempty"`
	// Requirement is the exact planning requirement this verifier run satisfies.
	// Legacy receipts omit it and are matched by their historical shape.
	Requirement string `json:"requirement,omitempty"`
	ClaimID     string `json:"claimId,omitempty"`
	Actor       string `json:"actor,omitempty"`
	// Contract binds a named claim to the exact spec, configured check, or
	// capability selector that produced its status.
	Contract string `json:"contract,omitempty"`
	Tool     string `json:"tool,omitempty"`
	// VerifierVersion records the verifier tool's version when known.
	VerifierVersion string             `json:"verifierVersion,omitempty"`
	Status          VerificationStatus `json:"status"`
	Evidence        []string           `json:"evidence,omitempty"` // evidence IDs
	Artifact        string             `json:"artifact,omitempty"` // fcheap://… reference
	// Sensitive labels the receipt (and its linked artifact) as possibly holding
	// sensitive material, so it isn't archived or shared carelessly.
	Sensitive bool   `json:"sensitive,omitempty"`
	Revision  string `json:"revision,omitempty"` // full git HEAD the proof pertains to
	// DirtyDigest binds the proof to the tracked diff + untracked content at
	// verifier runtime. Revision alone is insufficient when HEAD has not moved.
	DirtyDigest string              `json:"dirtyDigest,omitempty"`
	Binding     VerificationBinding `json:"binding,omitempty"`
	Notes       string              `json:"notes,omitempty"`
	Timestamp   time.Time           `json:"timestamp"`
}

// Validate enforces the invariant that a verification pass names its claim.
func (v VerificationRecord) Validate() error {
	if v.Claim == "" {
		return errValidation("verification record has no claim")
	}
	if v.Status == "" {
		return errValidation("verification record has no status")
	}
	if v.Purpose != "" && v.Purpose != VerificationPurposeVerifierRun && v.Purpose != VerificationPurposeNamedClaim {
		return errValidation("verification record has invalid purpose")
	}
	if v.Binding != "" && v.Binding != VerificationBound && v.Binding != VerificationUnbound {
		return errValidation("verification record has invalid binding")
	}
	return nil
}

// Proven reports whether the record is an affirmative pass bound to a stable
// workspace state. Legacy records with an empty binding retain compatibility.
func (v VerificationRecord) Proven() bool {
	return v.Status == VerifyPassed && v.Binding != VerificationUnbound
}

// Failed reports whether the record is a definitive failure bound to a stable
// workspace state. An unbound failure is inconclusive: the verifier may have
// observed an intermediate revision while another actor was editing.
func (v VerificationRecord) Failed() bool {
	return v.Status == VerifyFailed && v.Binding != VerificationUnbound
}

// Definitive reports whether the record carries a stable pass/fail verdict.
func (v VerificationRecord) Definitive() bool {
	return v.Proven() || v.Failed()
}

// EffectivePurpose returns the explicit purpose for new records and infers the
// purpose of a legacy record. Before purpose was persisted, verifier-run
// receipts used a small set of stable claim labels and normally carried direct
// evidence, an artifact, or a verifier version. The subsequent named-claim
// receipt carried none of those. Empty-claim records are treated as verifier
// runs for compatibility with early hand-authored/test fixtures.
func (v VerificationRecord) EffectivePurpose() VerificationPurpose {
	if v.Purpose != "" {
		return v.Purpose
	}
	claim := strings.ToLower(strings.TrimSpace(v.Claim))
	if claim == "" || legacyVerifierClaim(claim) || len(v.Evidence) > 0 || v.Artifact != "" || v.VerifierVersion != "" {
		return VerificationPurposeVerifierRun
	}
	return VerificationPurposeNamedClaim
}

func legacyVerifierClaim(claim string) bool {
	if claim == "structural review of the diff" {
		return true
	}
	for _, prefix := range []string{
		"browser flow ", "terminal flow ",
		"auto-selected browser flow ", "auto-selected terminal flow ",
		"artifact verification ", "secret verification ",
	} {
		if strings.HasPrefix(claim, prefix) {
			return true
		}
	}
	return false
}
