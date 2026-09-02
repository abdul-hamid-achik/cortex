package kernel

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// maxSurveyModules bounds the coverage ledger so a monorepo survey stays a
// finite, readable walk. Largest (most depended-upon) modules are kept.
const maxSurveyModules = 512

// surveyJunkDirs are never survey modules.
var surveyJunkDirs = map[string]bool{
	"node_modules": true, "dist": true, "build": true, "vendor": true, ".git": true,
	"coverage": true, "target": true, ".cortex": true, ".agent": true, "__pycache__": true,
}

// buildCoverageLedger derives the survey's module graph. codemap's
// architecture map supplies real fan-in; without it the git tree supplies
// directories and file counts stand in for fan-in (stated in warnings).
func (k *Kernel) buildCoverageLedger(ctx context.Context, c *domain.CaseFile) (domain.CoverageLedger, []string) {
	ledger := domain.CoverageLedger{SchemaVersion: 1, BuiltAt: k.now().UTC(), Commit: c.Workspace.CommitBefore}
	var warnings []string
	if cm, ok := k.reg.Get("codemap").(*adapters.Codemap); ok && cm != nil {
		mapCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		subsystems, err := cm.Map(mapCtx, k.cfg.Workspace)
		cancel()
		if err == nil && len(subsystems) > 0 {
			ledger.Source = "codemap_map"
			for _, s := range subsystems {
				path := domain.NormalizeModulePath(s.Name)
				if surveySkip(path) {
					continue
				}
				ledger.Modules = append(ledger.Modules, domain.ModuleCoverage{
					Path: path, Status: domain.ModuleUnseen, FanIn: s.InboundEdges, Files: s.Files, Symbols: s.Symbols,
				})
			}
		} else if err != nil && !isMissingAdapter(err) {
			warnings = append(warnings, "codemap architecture map unavailable ("+err.Error()+"); surveying the git tree instead — run `codemap index` for fan-in ordering")
		}
	}
	if len(ledger.Modules) == 0 && k.git != nil {
		files, err := k.git.TrackedFiles(ctx, k.cfg.Workspace)
		if err != nil {
			warnings = append(warnings, "could not list tracked files for the survey ledger: "+err.Error())
		} else {
			ledger.Source = "git_tree"
			counts := map[string]int{}
			for _, f := range files {
				m := surveyModuleOf(f)
				if m == "" {
					continue
				}
				counts[m]++
			}
			for path, n := range counts {
				ledger.Modules = append(ledger.Modules, domain.ModuleCoverage{Path: path, Status: domain.ModuleUnseen, FanIn: n, Files: n})
			}
			if len(ledger.Modules) > 0 {
				warnings = append(warnings, "survey ledger built from the git tree: fan-in is unknown, so modules are ordered by file count (index with codemap for dependency order)")
			}
		}
	}
	ledger.Sort()
	if len(ledger.Modules) > maxSurveyModules {
		warnings = append(warnings, fmt.Sprintf("survey ledger bounded to the %d largest of %d modules", maxSurveyModules, len(ledger.Modules)))
		ledger.Modules = ledger.Modules[:maxSurveyModules]
	}
	return ledger, warnings
}

func surveySkip(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if surveyJunkDirs[seg] {
			return true
		}
	}
	return adapters.JunkDiscoveryPath(path)
}

// surveyModuleOf maps a tracked file to its survey module: the first two
// path segments when the file is nested that deep, the first segment for
// shallow trees, and "." for root files. Junk directories yield "".
func surveyModuleOf(file string) string {
	file = domain.NormalizeModulePath(file)
	segs := strings.Split(file, "/")
	if len(segs) == 1 {
		return "."
	}
	depth := 1
	if len(segs) >= 3 {
		depth = 2
	}
	module := strings.Join(segs[:depth], "/")
	if surveySkip(module) {
		return ""
	}
	return module
}

// coverageFor loads the survey ledger; nil when the case has none.
func (k *Kernel) coverageFor(taskID string) *domain.CoverageLedger {
	ledger, err := k.store.LoadCoverage(taskID)
	if err != nil {
		return nil
	}
	return &ledger
}

func (k *Kernel) coverageModuleCandidates(taskID string) []string {
	ledger := k.coverageFor(taskID)
	if ledger == nil {
		return nil
	}
	var out []string
	for _, m := range ledger.Modules {
		out = append(out, m.Path)
		if len(out) >= maxRejectionCandidates {
			break
		}
	}
	return out
}

// markModuleVisited records one investigation round scoped to a module. A
// module the agent names that is not in the ledger is appended — the ledger
// is a guide, not a fence — so coverage stays honest about what was looked at.
func (k *Kernel) markModuleVisited(taskID, module string, evidence int) (*domain.ModuleCoverage, error) {
	module = domain.NormalizeModulePath(module)
	now := k.now().UTC()
	var visited domain.ModuleCoverage
	_, err := k.store.UpdateCoverage(taskID, func(l *domain.CoverageLedger) error {
		row := l.Find(module)
		if row == nil {
			row = l.Owner(module)
		}
		if row == nil {
			l.Modules = append(l.Modules, domain.ModuleCoverage{Path: module, Status: domain.ModuleUnseen})
			row = &l.Modules[len(l.Modules)-1]
		}
		row.Rounds++
		row.EvidenceCount += evidence
		row.LastVisited = &now
		if row.Status == domain.ModuleUnseen {
			row.Status = domain.ModuleExplored
		}
		visited = *row
		return nil
	})
	if err != nil {
		if err == casefs.ErrNoCoverage {
			return nil, nil
		}
		return nil, err
	}
	return &visited, nil
}

// markModuleSummarized links a dossier entry to its module and promotes the
// module to summarized. Best effort; non-survey cases have no ledger.
func (k *Kernel) markModuleSummarized(taskID, module, entryID string) {
	module = domain.NormalizeModulePath(module)
	_, _ = k.store.UpdateCoverage(taskID, func(l *domain.CoverageLedger) error {
		row := l.Find(module)
		if row == nil {
			row = l.Owner(module)
		}
		if row == nil {
			return nil
		}
		row.Status = domain.ModuleSummarized
		for _, id := range row.DossierEntryIDs {
			if id == entryID {
				return nil
			}
		}
		row.DossierEntryIDs = append(row.DossierEntryIDs, entryID)
		return nil
	})
}

// surveyActions are the structured continuations a survey case offers while
// investigating: visit the next module, then promote what was learned.
func (k *Kernel) surveyActions(c *domain.CaseFile, ledger *domain.CoverageLedger) []domain.NextAction {
	if c == nil || ledger == nil || c.Status != domain.PhaseInvestigating {
		return nil
	}
	var out []domain.NextAction
	if next := ledger.Next(); next != nil {
		args := knownActionArgs(c)
		args["module"] = next.Path
		args["depth"] = "standard"
		out = append(out, domain.NextAction{
			Tool: "cortex_investigate", Command: cortexCommand(c, "investigate", c.ID, "QUESTION", "--module", next.Path),
			Reason:    fmt.Sprintf("survey the next uncovered module (%s, fan-in %d)", next.Path, next.FanIn),
			Arguments: args, Inputs: []string{"question"},
		})
	}
	var lastVisited *domain.ModuleCoverage
	for i := range ledger.Modules {
		m := &ledger.Modules[i]
		if m.Status == domain.ModuleExplored && m.LastVisited != nil && (lastVisited == nil || m.LastVisited.After(*lastVisited.LastVisited)) {
			lastVisited = m
		}
	}
	if lastVisited != nil {
		out = append(out, domain.NextAction{
			Tool: "cortex_dossier", Command: cortexCommand(c, "dossier", "add", c.ID, "--module", lastVisited.Path, "--title", "TITLE", "--summary", "SUMMARY"),
			Reason:    fmt.Sprintf("promote what %s does and its invariants into repository memory", lastVisited.Path),
			Arguments: cloneArgs(knownActionArgs(c), "operation", "add", "module", lastVisited.Path), Inputs: []string{"title", "summary"},
		})
	}
	summary := ledger.Summary()
	if summary.Unseen == 0 && summary.Total > 0 {
		out = append(out, domain.NextAction{
			Tool: "cortex_plan", Command: cortexCommand(c, "plan", c.ID),
			Reason:    "every module has been visited; state what you now believe (with disproof paths) before closing the survey",
			Arguments: knownActionArgs(c), Inputs: []string{"hypotheses", "uncertainty"},
		})
	}
	return out
}

// surveyComplete reports whether every ledger module was at least explored.
func surveyComplete(ledger *domain.CoverageLedger) bool {
	if ledger == nil || len(ledger.Modules) == 0 {
		return false
	}
	return ledger.Summary().Unseen == 0
}

// CoverageReport is the survey progress view.
type CoverageReport struct {
	domain.Envelope
	Coverage domain.CoverageSummary  `json:"coverage"`
	Modules  []domain.ModuleCoverage `json:"modules,omitempty"`
}

// Coverage returns the ledger with progress; unseen modules first.
func (k *Kernel) Coverage(taskID string, all bool) (CoverageReport, error) {
	c, err := k.store.Load(taskID)
	if err != nil {
		return CoverageReport{Envelope: errEnvelope(taskID, err.Error())}, nil
	}
	ledger := k.coverageFor(c.ID)
	if ledger == nil {
		return CoverageReport{Envelope: k.errEnvelopeForCase(c, "this case has no coverage ledger (open it with --mode survey)")}, nil
	}
	modules := append([]domain.ModuleCoverage(nil), ledger.Modules...)
	sort.SliceStable(modules, func(i, j int) bool {
		rank := func(s domain.ModuleCoverageStatus) int {
			switch s {
			case domain.ModuleUnseen:
				return 0
			case domain.ModuleExplored:
				return 1
			default:
				return 2
			}
		}
		if rank(modules[i].Status) != rank(modules[j].Status) {
			return rank(modules[i].Status) < rank(modules[j].Status)
		}
		return modules[i].FanIn > modules[j].FanIn
	})
	if !all && len(modules) > 50 {
		modules = modules[:50]
	}
	summary := ledger.Summary()
	rep := CoverageReport{
		Envelope: domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status,
			Summary: fmt.Sprintf("survey coverage %.0f%%: %d/%d modules explored, %d summarized, %d unseen (source %s)",
				summary.Percent, summary.Explored+summary.Summarized, summary.Total, summary.Summarized, summary.Unseen, summary.Source)},
		Coverage: summary, Modules: modules,
	}
	rep.Actions = k.surveyActions(c, ledger)
	if !all && len(ledger.Modules) > 50 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("showing 50 of %d modules; pass all=true for the full ledger", len(ledger.Modules)))
	}
	return rep, nil
}
