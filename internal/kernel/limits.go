package kernel

// Durable user/model text is bounded at write time. These limits are generous
// for reasoning records but prevent one malformed call from turning every
// later Status, Show, Studio, or handoff read into an unbounded allocation.
const (
	maxGoalBytes             = 4 << 10
	maxRecordTextBytes       = 16 << 10
	maxLocatorBytes          = 4 << 10
	maxPlanHypotheses        = 64
	maxHypothesisSupports    = 256
	maxBoundaryEntries       = 512
	maxDecisionOptions       = 16
	maxStableIdentifierBytes = 256

	// maxRejectionCandidates bounds how many real values (evidence IDs,
	// candidate reasons, …) a rejection's structured Actions offers per input —
	// enough to be genuinely useful without copying a long ledger into every
	// error response.
	maxRejectionCandidates = 20

	// Completion summaries are intentionally much smaller than casefs's 1 MiB
	// hard limit. Valid maximum-size plans must always remain completable, and a
	// long append-only evidence ledger must not be copied wholesale into Markdown.
	maxCompletionSummaryBytes      = 512 << 10
	maxCompletionSummaryEvidence   = 200
	maxCompletionSummaryReceipts   = 100
	maxCompletionSummaryHypotheses = 64
)

func textExceeds(value string, max int) bool { return len(value) > max }
