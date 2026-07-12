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
)

func textExceeds(value string, max int) bool { return len(value) > max }
