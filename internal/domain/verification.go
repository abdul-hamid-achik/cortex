package domain

import "time"

// VerificationStatus is the outcome of a verification attempt (SPEC §14.2).
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

// VerificationRecord is a structured proof record for a specific claim
// (SPEC §8.5, §14.3). Every record names the exact claim it supports.
type VerificationRecord struct {
	ID      string  `json:"id"`
	Claim   string  `json:"claim"`
	Surface Surface `json:"surface"`
	Tool    string  `json:"tool,omitempty"`
	// VerifierVersion records the verifier tool's version when known (SPEC §14.3).
	VerifierVersion string             `json:"verifierVersion,omitempty"`
	Status          VerificationStatus `json:"status"`
	Evidence        []string           `json:"evidence,omitempty"` // evidence IDs
	Artifact        string             `json:"artifact,omitempty"` // fcheap://… reference
	// Sensitive labels the receipt (and its linked artifact) as possibly holding
	// sensitive material, so it isn't archived or shared carelessly (SPEC §16.2 #5).
	Sensitive bool   `json:"sensitive,omitempty"`
	Revision  string `json:"revision,omitempty"` // full git HEAD the proof pertains to
	// DirtyDigest binds the proof to the tracked diff + untracked content at
	// verifier runtime. Revision alone is insufficient when HEAD has not moved.
	DirtyDigest string    `json:"dirtyDigest,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// Validate enforces the invariant that a verification pass names its claim
// (SPEC §6.3 #5).
func (v VerificationRecord) Validate() error {
	if v.Claim == "" {
		return errValidation("verification record has no claim")
	}
	if v.Status == "" {
		return errValidation("verification record has no status")
	}
	return nil
}

// Proven reports whether the record is an affirmative pass.
func (v VerificationRecord) Proven() bool { return v.Status == VerifyPassed }
