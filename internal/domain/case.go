// Package domain holds Cortex's core types: the case file and its records
// (evidence, hypotheses, plans, verifications) plus the phase machine and
// policy bands that make tool use stateful and evidence-driven. It has no
// dependency on adapters, storage, or transport — those layers depend on it.
package domain

import (
	"fmt"
	"time"
)

// SchemaVersion is the on-disk case-file schema version. Bump it (and add a
// migration in store/casefs) on any breaking change to the JSON layout.
const SchemaVersion = 1

// Phase is a task lifecycle state (SPEC §6.1).
type Phase string

const (
	PhaseNew           Phase = "new"
	PhaseOrienting     Phase = "orienting"
	PhaseInvestigating Phase = "investigating"
	PhasePlanned       Phase = "planned"
	PhaseChanging      Phase = "changing"
	PhaseVerifying     Phase = "verifying"
	PhasePersisting    Phase = "persisting"
	PhaseComplete      Phase = "complete"

	// Terminal alternatives.
	PhaseBlocked            Phase = "blocked"
	PhaseAbandoned          Phase = "abandoned"
	PhaseNeedsHumanDecision Phase = "needs_human_decision"
)

// Mode describes the kind of work a task represents.
type Mode string

const (
	ModeChange      Mode = "change"      // will mutate the workspace
	ModeInvestigate Mode = "investigate" // read-only understanding
	ModeReview      Mode = "review"      // diff-scoped analysis
)

// Surface is a user-visible system layer a change can affect (SPEC §3.6).
type Surface string

const (
	SurfaceCode     Surface = "code"
	SurfaceBrowser  Surface = "browser"
	SurfaceTerminal Surface = "terminal"
	SurfaceArtifact Surface = "artifact"
	SurfaceSecret   Surface = "secret"
)

// Workspace records repository identity and baseline VCS context.
type Workspace struct {
	Root         string `json:"root"`
	Repository   string `json:"repository,omitempty"`
	Branch       string `json:"branch,omitempty"`
	CommitBefore string `json:"commitBefore,omitempty"`
	// BaseRef is the diff base for a review task (e.g. the PR base or a branch's
	// merge-base). When set, diff-scoped tools compare base…HEAD instead of the
	// working tree. Empty for a change task (which diffs the working tree).
	BaseRef string `json:"baseRef,omitempty"`
}

// ChangeBoundary is the declared set of expected modifications (SPEC §3.5). It
// is a reasoning guardrail, not a security boundary.
type ChangeBoundary struct {
	Files   []string `json:"files,omitempty"`
	Symbols []string `json:"symbols,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

// Declared reports whether a boundary has any files or symbols set.
func (b ChangeBoundary) Declared() bool {
	return len(b.Files) > 0 || len(b.Symbols) > 0
}

// CaseFile is the durable state of one task (SPEC §8.2). It is working memory,
// not a transcript.
type CaseFile struct {
	SchemaVersion  int            `json:"schemaVersion"`
	ID             string         `json:"id"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	Goal           string         `json:"goal"`
	Mode           Mode           `json:"mode"`
	Status         Phase          `json:"status"`
	Risk           string         `json:"risk,omitempty"` // low | medium | high
	Workspace      Workspace      `json:"workspace"`
	Surfaces       []Surface      `json:"surfaces,omitempty"`
	ChangeBoundary ChangeBoundary `json:"changeBoundary,omitempty"`
	// VerificationRequired names the verifier claims a task must satisfy before
	// it can be considered complete (populated at plan time).
	VerificationRequired []string `json:"verificationRequired,omitempty"`
	// BlockedReason is set when Status is a terminal blocked/abandoned state.
	BlockedReason string `json:"blockedReason,omitempty"`
	// InvestigationRounds counts cortex_investigate calls, checked against the
	// budget to discourage frantic, indiscriminate tool use (SPEC §7.3).
	InvestigationRounds int `json:"investigationRounds,omitempty"`
	// Notes carries free-form orientation facts (tool health, git state) and any
	// recorded reason for exceeding the investigation budget (SPEC §7.3).
	Notes []string `json:"notes,omitempty"`
}

// HasSurface reports whether the case involves the given verification surface.
func (c *CaseFile) HasSurface(s Surface) bool {
	for _, x := range c.Surfaces {
		if x == s {
			return true
		}
	}
	return false
}

// transitions is the legal phase graph (SPEC §6.2). A move is allowed only when
// the source phase lists the destination. Terminal states (blocked/abandoned/
// needs_human_decision) are reachable from any phase and handled separately.
var transitions = map[Phase][]Phase{
	PhaseNew:           {PhaseOrienting},
	PhaseOrienting:     {PhaseInvestigating},
	PhaseInvestigating: {PhasePlanned},
	PhasePlanned:       {PhaseChanging, PhaseVerifying}, // investigate-only tasks skip changing
	PhaseChanging:      {PhaseVerifying},
	PhaseVerifying:     {PhasePersisting, PhaseChanging}, // failed verify can loop back
	PhasePersisting:    {PhaseComplete},
}

// terminalPhases can be entered from any non-terminal phase.
var terminalPhases = map[Phase]bool{
	PhaseBlocked:            true,
	PhaseAbandoned:          true,
	PhaseNeedsHumanDecision: true,
}

// IsTerminal reports whether a phase is a stop state (complete or a blocked
// alternative) from which no further work proceeds.
func (p Phase) IsTerminal() bool {
	return p == PhaseComplete || terminalPhases[p]
}

// CanTransition reports whether moving from `from` to `to` is structurally
// legal, ignoring the data-precondition invariants checked elsewhere.
func CanTransition(from, to Phase) bool {
	if terminalPhases[to] {
		return !from.IsTerminal()
	}
	for _, allowed := range transitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// ErrIllegalTransition describes a rejected phase move.
type ErrIllegalTransition struct {
	From, To Phase
	Reason   string
}

func (e ErrIllegalTransition) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("illegal transition %s → %s: %s", e.From, e.To, e.Reason)
	}
	return fmt.Sprintf("illegal transition %s → %s", e.From, e.To)
}
