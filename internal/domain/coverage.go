package domain

import (
	"sort"
	"strings"
	"time"
)

// ModuleCoverageStatus is how far a survey has taken one module.
type ModuleCoverageStatus string

const (
	ModuleUnseen     ModuleCoverageStatus = "unseen"
	ModuleExplored   ModuleCoverageStatus = "explored"   // at least one investigation round scoped to it
	ModuleSummarized ModuleCoverageStatus = "summarized" // at least one dossier entry written for it
)

// ModuleCoverage is one row of the survey coverage ledger.
type ModuleCoverage struct {
	Path            string               `json:"path"`
	Status          ModuleCoverageStatus `json:"status"`
	FanIn           int                  `json:"fanIn"`
	Files           int                  `json:"files,omitempty"`
	Symbols         int                  `json:"symbols,omitempty"`
	Rounds          int                  `json:"rounds,omitempty"`
	EvidenceCount   int                  `json:"evidenceCount,omitempty"`
	LastVisited     *time.Time           `json:"lastVisited,omitempty"`
	DossierEntryIDs []string             `json:"dossierEntryIds,omitempty"`
}

// CoverageLedger turns "understand the whole repository" into a finite,
// resumable walk over a module graph. It is built once at survey open time
// from codemap's architecture map (or the git tree when codemap is absent).
type CoverageLedger struct {
	SchemaVersion int              `json:"schemaVersion"`
	Source        string           `json:"source"` // codemap_map | git_tree
	BuiltAt       time.Time        `json:"builtAt"`
	Commit        string           `json:"commit,omitempty"`
	Modules       []ModuleCoverage `json:"modules"`
}

// CoverageSummary is the bounded progress view status reports.
type CoverageSummary struct {
	Source     string  `json:"source"`
	Total      int     `json:"total"`
	Unseen     int     `json:"unseen"`
	Explored   int     `json:"explored"`
	Summarized int     `json:"summarized"`
	Percent    float64 `json:"percent"` // modules explored or summarized, 0–100
	Next       string  `json:"next,omitempty"`
}

// Summary computes coverage progress. Percent counts a module as covered once
// it has been explored; summarized is the stronger, dossier-backed state.
func (l CoverageLedger) Summary() CoverageSummary {
	s := CoverageSummary{Source: l.Source, Total: len(l.Modules)}
	for _, m := range l.Modules {
		switch m.Status {
		case ModuleExplored:
			s.Explored++
		case ModuleSummarized:
			s.Summarized++
		default:
			s.Unseen++
		}
	}
	if s.Total > 0 {
		s.Percent = float64(s.Explored+s.Summarized) * 100 / float64(s.Total)
	}
	if next := l.Next(); next != nil {
		s.Next = next.Path
	}
	return s
}

// Next picks the module a survey should visit next: the unseen module with the
// highest fan-in (most depended-upon first), then, once every module has been
// seen, the explored module with the fewest rounds. Nil when nothing is left.
func (l CoverageLedger) Next() *ModuleCoverage {
	var best *ModuleCoverage
	for i := range l.Modules {
		m := &l.Modules[i]
		if m.Status != ModuleUnseen {
			continue
		}
		if best == nil || m.FanIn > best.FanIn || (m.FanIn == best.FanIn && m.Path < best.Path) {
			best = m
		}
	}
	if best != nil {
		return best
	}
	for i := range l.Modules {
		m := &l.Modules[i]
		if m.Status != ModuleExplored {
			continue
		}
		if best == nil || m.Rounds < best.Rounds || (m.Rounds == best.Rounds && m.FanIn > best.FanIn) {
			best = m
		}
	}
	return best
}

// Find returns the module row for a path (exact, slash-normalized) or nil.
func (l *CoverageLedger) Find(path string) *ModuleCoverage {
	path = NormalizeModulePath(path)
	for i := range l.Modules {
		if l.Modules[i].Path == path {
			return &l.Modules[i]
		}
	}
	return nil
}

// Owner returns the module row whose path is the longest prefix of file, or
// nil when no module contains it.
func (l *CoverageLedger) Owner(file string) *ModuleCoverage {
	file = NormalizeModulePath(file)
	var best *ModuleCoverage
	for i := range l.Modules {
		p := l.Modules[i].Path
		if p == "." || file == p || strings.HasPrefix(file, p+"/") {
			if best == nil || len(p) > len(best.Path) {
				best = &l.Modules[i]
			}
		}
	}
	return best
}

// Sort orders modules by fan-in descending, then path, so ledgers are stable.
func (l *CoverageLedger) Sort() {
	sort.SliceStable(l.Modules, func(i, j int) bool {
		if l.Modules[i].FanIn != l.Modules[j].FanIn {
			return l.Modules[i].FanIn > l.Modules[j].FanIn
		}
		return l.Modules[i].Path < l.Modules[j].Path
	})
}

// NormalizeModulePath canonicalizes a module or file path to forward slashes
// without leading "./" or trailing "/".
func NormalizeModulePath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	if p == "" {
		return "."
	}
	return p
}
