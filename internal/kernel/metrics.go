package kernel

import (
	"sort"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// TaskMetrics is the per-task observability summary (SPEC §18.1/§18.2): it
// measures outcomes and the evidence trail, not merely tool-call volume. It is
// computed from the case file plus the previously write-only audit log.
type TaskMetrics struct {
	TaskID               string             `json:"taskId"`
	Goal                 string             `json:"goal"`
	Status               string             `json:"status"`
	Complete             bool               `json:"complete"`
	Verified             bool               `json:"verified"` // complete AND every required verifier passed
	ToolCalls            int                `json:"toolCalls"`
	ToolErrors           int                `json:"toolErrors"`
	CallsBeforeEvidence  int                `json:"callsBeforeFirstEvidence"`
	EvidenceItems        int                `json:"evidenceItems"`
	InvestigationRounds  int                `json:"investigationRounds"`
	Hypotheses           int                `json:"hypotheses"`
	UnresolvedHypotheses int                `json:"unresolvedHypotheses"`
	Surfaces             []string           `json:"surfaces,omitempty"`
	VerifiedSurfaces     []string           `json:"verifiedSurfaces,omitempty"`
	MissingVerification  []string           `json:"missingVerification,omitempty"`
	ScopeDrifted         bool               `json:"scopeDrifted"`
	MemoryReused         bool               `json:"memoryReused"`
	ToolContribution     []ToolContribution `json:"toolContribution,omitempty"`
}

// ToolContribution enriches mcphub's raw call counts with task-level meaning
// (SPEC §18.2): not "codemap called 8×" but "codemap contributed evidence to N
// hypotheses".
type ToolContribution struct {
	Tool                string `json:"tool"`
	Calls               int    `json:"calls"`
	Errors              int    `json:"errors"`
	EvidenceItems       int    `json:"evidenceItems"`
	HypothesesSupported int    `json:"hypothesesSupported"`
}

// TaskMetrics computes the observability summary for one task.
func (k *Kernel) TaskMetrics(taskID string) (TaskMetrics, error) {
	c, err := k.store.Load(taskID)
	if err != nil {
		return TaskMetrics{}, err
	}
	cmds, _ := k.store.Commands(taskID)
	evidence, _ := k.store.Evidence(taskID)
	hyps, _ := k.store.Hypotheses(taskID)
	receipts, _ := k.store.Verifications(taskID)

	m := TaskMetrics{
		TaskID: c.ID, Goal: c.Goal, Status: string(c.Status),
		Complete:            c.Status == domain.PhaseComplete,
		ToolCalls:           len(cmds),
		EvidenceItems:       len(evidence),
		InvestigationRounds: c.InvestigationRounds,
		Hypotheses:          len(hyps),
		Surfaces:            surfaceNames(c.Surfaces),
	}

	perTool := map[string]*ToolContribution{}
	tc := func(tool string) *ToolContribution {
		if p := perTool[tool]; p != nil {
			return p
		}
		p := &ToolContribution{Tool: tool}
		perTool[tool] = p
		return p
	}
	for _, cmd := range cmds {
		t := tc(cmd.Tool)
		t.Calls++
		if cmd.Status == string(domain.VerifyFailed) || cmd.Status == "error" || cmd.Status == "unavailable" {
			t.Errors++
			m.ToolErrors++
		}
	}

	// Tool calls before the first INVESTIGATION evidence (a "thrash" proxy). The
	// git orientation record is stamped at task creation (before any tool call),
	// so it must be excluded or the count is pinned to 0 for every git workspace.
	firstEv, haveEv := time.Time{}, false
	for _, ev := range evidence {
		if ev.Source.Tool != "git" { // git = orientation + scope-drift, not discovery
			firstEv, haveEv = ev.Timestamp, true
			break
		}
	}
	if haveEv {
		for _, cmd := range cmds {
			if cmd.Timestamp.Before(firstEv) {
				m.CallsBeforeEvidence++
			}
		}
	} else {
		m.CallsBeforeEvidence = len(cmds)
	}

	// Per-tool evidence contribution, plus memory-reuse / scope-drift signals.
	evByTool := map[string]map[string]bool{}
	for _, ev := range evidence {
		tool := ev.Source.Tool
		tc(tool).EvidenceItems++
		if evByTool[tool] == nil {
			evByTool[tool] = map[string]bool{}
		}
		evByTool[tool][ev.ID] = true
		if strings.HasPrefix(ev.Claim, "prior memory") {
			m.MemoryReused = true
		}
		if strings.HasPrefix(ev.Claim, "scope drift") {
			m.ScopeDrifted = true
		}
	}
	// §18.2: how many hypotheses each tool's evidence supports.
	for tool, ids := range evByTool {
		n := 0
		for _, h := range hyps {
			for _, sup := range h.Supports {
				if ids[sup] {
					n++
					break
				}
			}
		}
		tc(tool).HypothesesSupported = n
	}

	for _, h := range hyps {
		if h.Status == domain.HypActive || h.Status == domain.HypChallenged {
			m.UnresolvedHypotheses++
		}
	}
	for _, r := range receipts {
		if r.Proven() {
			m.VerifiedSurfaces = appendUnique(m.VerifiedSurfaces, string(r.Surface))
		}
	}
	for _, req := range c.VerificationRequired {
		if !verifierSatisfied(req, receipts) {
			m.MissingVerification = append(m.MissingVerification, req)
		}
	}
	m.Verified = m.Complete && len(m.MissingVerification) == 0 && anyProven(receipts)
	m.ToolContribution = sortContributions(perTool)
	return m, nil
}

// WorkspaceMetrics aggregates TaskMetrics across every task in the workspace
// (SPEC §18.1 core metrics). Rates are 0..1.
type WorkspaceMetrics struct {
	Tasks                     int            `json:"tasks"`
	Completed                 int            `json:"completed"`
	VerifiedCompletions       int            `json:"verifiedCompletions"`
	CompletionRate            float64        `json:"completionRate"`
	VerifiedCompletionRate    float64        `json:"verifiedCompletionRate"`
	MeanToolsPerCompletedTask float64        `json:"meanToolsPerCompletedTask"`
	ScopeDriftRate            float64        `json:"scopeDriftRate"`
	UnresolvedHypothesisRate  float64        `json:"unresolvedHypothesisRate"`
	MemoryReuseRate           float64        `json:"memoryReuseRate"`
	ToolCalls                 map[string]int `json:"toolCalls,omitempty"`
}

// WorkspaceMetrics aggregates per-task metrics over all tasks.
func (k *Kernel) WorkspaceMetrics() (WorkspaceMetrics, []TaskMetrics, error) {
	ids, err := k.store.List()
	if err != nil {
		return WorkspaceMetrics{}, nil, err
	}
	var wm WorkspaceMetrics
	wm.ToolCalls = map[string]int{}
	var per []TaskMetrics
	toolsInCompleted, drift, unresolved, memReuse := 0, 0, 0, 0
	for _, id := range ids {
		tm, err := k.TaskMetrics(id)
		if err != nil {
			continue
		}
		per = append(per, tm)
		wm.Tasks++
		if tm.Complete {
			wm.Completed++
			toolsInCompleted += tm.ToolCalls
		}
		if tm.Verified {
			wm.VerifiedCompletions++
		}
		if tm.ScopeDrifted {
			drift++
		}
		if tm.Hypotheses > 0 && tm.UnresolvedHypotheses > 0 {
			unresolved++
		}
		if tm.MemoryReused {
			memReuse++
		}
		for _, tc := range tm.ToolContribution {
			wm.ToolCalls[tc.Tool] += tc.Calls
		}
	}
	if wm.Tasks > 0 {
		wm.CompletionRate = ratio(wm.Completed, wm.Tasks)
		wm.VerifiedCompletionRate = ratio(wm.VerifiedCompletions, wm.Tasks)
		wm.ScopeDriftRate = ratio(drift, wm.Tasks)
		wm.UnresolvedHypothesisRate = ratio(unresolved, wm.Tasks)
		wm.MemoryReuseRate = ratio(memReuse, wm.Tasks)
	}
	if wm.Completed > 0 {
		wm.MeanToolsPerCompletedTask = float64(toolsInCompleted) / float64(wm.Completed)
	}
	return wm, per, nil
}

func anyProven(receipts []domain.VerificationRecord) bool {
	for _, r := range receipts {
		if r.Proven() {
			return true
		}
	}
	return false
}

func appendUnique(xs []string, x string) []string {
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}

func sortContributions(m map[string]*ToolContribution) []ToolContribution {
	out := make([]ToolContribution, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HypothesesSupported != out[j].HypothesesSupported {
			return out[i].HypothesesSupported > out[j].HypothesesSupported
		}
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Tool < out[j].Tool
	})
	return out
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
