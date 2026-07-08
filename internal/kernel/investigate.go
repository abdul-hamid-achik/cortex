package kernel

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// InvestigateInput parameterizes Investigate (SPEC §10.2 cortex_investigate).
type InvestigateInput struct {
	TaskID   string
	Question string
	Surfaces []domain.Surface
	Depth    string // quick | standard | deep
	// Video, when set, is a bug-video bundle path or vidtrace stash id: Cortex
	// runs vidtrace to turn the recording into timestamped evidence and link the
	// visible failure to code (SPEC §19.4 investigate-a-bug-video).
	Video string
}

// Investigate routes a question through the appropriate discovery and
// structural tools (SPEC §7.1), records the returned evidence, and returns a
// bounded investigation summary. Search results are recorded as candidates, not
// proof (SPEC §5.2).
func (k *Kernel) Investigate(ctx context.Context, in InvestigateInput) (domain.Envelope, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	if in.Question == "" {
		return errEnvelope(in.TaskID, "investigate needs a question"), nil
	}
	if c.Status != domain.PhaseInvestigating && c.Status != domain.PhasePlanned {
		return errEnvelope(in.TaskID, fmt.Sprintf("cannot investigate in phase %q; investigate happens after start", c.Status)), nil
	}
	// A raw recording passed straight to vidtrace fails with an opaque "bundle
	// validation failed: 0/1 checks passed" — fail fast here with guidance
	// instead (dogfooding 2026-07-07: this cost a manual vidtrace-CLI detour).
	if in.Video != "" && looksLikeRawVideo(in.Video) {
		return errEnvelope(in.TaskID, fmt.Sprintf(
			"video %q looks like a raw recording, not a vidtrace bundle — cortex_investigate does not extract "+
				"video itself; run `vidtrace extract %s -json` to build an evidence bundle, then pass the "+
				"resulting bundle directory (not the raw file) as video", in.Video, in.Video)), nil
	}

	surfaces := in.Surfaces
	if len(surfaces) == 0 {
		surfaces = c.Surfaces
	}
	route := domain.RouteFor(in.Question, surfaces)
	candLimit := k.cfg.Budget.MaxCandidateFilesReturned

	var facts []domain.Evidence
	var warnings []string
	degraded := false
	budget := k.cfg.Budget.MaxEvidenceItemsReturned

	// Count this round against the investigation budget (SPEC §7.3). Exceeding it
	// is allowed but recorded — the point is to discourage frantic search, not to
	// hard-block a legitimately deep investigation.
	c.InvestigationRounds++
	if maxRounds := k.cfg.Budget.MaxInvestigationRounds; maxRounds > 0 && c.InvestigationRounds > maxRounds {
		note := fmt.Sprintf("investigation round %d exceeds the budget of %d — consider forming a hypothesis and planning, or state why deeper investigation is needed", c.InvestigationRounds, maxRounds)
		warnings = append(warnings, note)
		c.Notes = append(c.Notes, "budget: "+note)
	}

	// Execute the routed tool sequence (first, then follow-up if distinct). An
	// explicit bug-video bundle runs a vidtrace investigation up front, linking
	// the visible failure to code before the usual discovery→structure route.
	steps := routeSteps(route, in.Question, surfaces, candLimit)
	// Recall prior durable conclusions for THIS repo first (SPEC §15.1 semantic
	// recall): cortex writes a memory on every completed task, so a related past
	// case ("returnTo was dropped in HandleCallback…") becomes low-confidence
	// orientation instead of being re-derived. Scoped by the cortex+repo tags the
	// persist phase writes; recall is global, so it needs no workspace index.
	steps = append([]step{{tool: "vecgrep", op: "memory_recall", input: map[string]any{
		"query": in.Question, "tags": []string{"cortex", c.Workspace.Repository}, "limit": candLimit,
	}}}, steps...)
	if in.Video != "" {
		steps = append([]step{{tool: "vidtrace", op: "investigate", input: map[string]any{
			"query": in.Question, "bundle": videoBundle(in.Video), "stash": videoStash(in.Video),
			"connect": true, "limit": candLimit,
		}}}, steps...)
	}
	// Execute the routed tool sequence with bounded parallelism (SPEC §7.3
	// max_parallel_calls). The steps are independent adapter calls — step N's
	// result does not feed step N+1's input — so they can run concurrently.
	// Results are collected back in step order so the evidence/warnings order
	// stays deterministic. The expensive part (subprocess spawn) parallelizes;
	// evidence stamping runs sequentially after, serializing store writes.
	results := k.runStepsParallel(ctx, c.ID, steps)
	for _, res := range results {
		// A non-authoritative result (partial/unavailable/error) means this step's
		// facts (if any) are stale, incomplete, or a fallback — never silently as
		// trustworthy as a clean search. Surfaced as a top-level flag, not just a
		// warning string the caller has to notice (dogfooding 2026-07-07).
		if res.Status != adapters.StatusAuthoritative {
			degraded = true
		}
		warnings = append(warnings, res.Warnings...)
		rawRef := k.storeRaw(c.ID, res) // one stored blob per tool call, shared by its facts
		for _, f := range res.Facts {
			if len(facts) >= budget {
				warnings = append(warnings, fmt.Sprintf("evidence truncated to %d items (budget)", budget))
				break
			}
			ev, err := k.stampEvidenceRaw(c.ID, res.Tool, f, rawRef)
			if err != nil {
				return errEnvelope(c.ID, err.Error()), err
			}
			facts = append(facts, ev)
		}
	}

	if err := k.store.Save(c); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}

	summary := fmt.Sprintf("investigated %q via %s→%s: %s recorded (%s)",
		clipStr(in.Question, 60), route.First, route.FollowUp, pluralizeEv(len(facts)), route.Why)
	if degraded {
		warnings = append([]string{"degraded: one or more discovery tools did not return an authoritative result this round — treat facts below with extra caution, not as a clean search"}, warnings...)
	}
	next := []string{
		"read raw evidence with cortex read-evidence <taskId> <evidenceId> when you need detail",
		"cortex plan — state a hypothesis with a disproof path, a change boundary, and a verification plan",
	}
	env := k.envelope(c, summary, facts, dedupeStr(warnings), next)
	env.Degraded = degraded
	// Surface the current hypotheses (if any) so status is coherent.
	if hs, _ := k.store.Hypotheses(c.ID); len(hs) > 0 {
		for _, h := range hs {
			env.Hypotheses = append(env.Hypotheses, domain.ToHypView(h))
		}
	}
	return env, nil
}

type step struct {
	tool  string
	op    string
	input map[string]any
}

// runStepsParallel executes the investigation steps concurrently, bounded by
// max_parallel_calls (SPEC §7.3). Steps are independent adapter calls, so they
// can fan out; results are returned in the original step order so evidence and
// warnings stay deterministic. A non-positive budget runs sequentially.
func (k *Kernel) runStepsParallel(ctx context.Context, taskID string, steps []step) []adapters.Result {
	results := make([]adapters.Result, len(steps))
	maxParallel := k.cfg.Budget.MaxParallelCalls
	if maxParallel < 1 || len(steps) <= 1 {
		for i, s := range steps {
			results[i] = k.run(ctx, s.tool, adapters.Request{TaskID: taskID, Operation: s.op, Input: s.input})
		}
		return results
	}
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	for i, s := range steps {
		wg.Add(1)
		go func(i int, s step) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = k.run(ctx, s.tool, adapters.Request{TaskID: taskID, Operation: s.op, Input: s.input})
		}(i, s)
	}
	wg.Wait()
	return results
}

// routeSteps expands a Route into concrete adapter operations for the question.
// Discovery searches are capped at candLimit candidate hits (SPEC §7.3
// max_candidate_files_returned) so one broad search can't flood the ledger.
func routeSteps(r domain.Route, question string, surfaces []domain.Surface, candLimit int) []step {
	if candLimit < 1 {
		candLimit = 8
	}
	var steps []step
	search := func(tool string) step {
		return step{tool: tool, op: "search", input: map[string]any{"query": question, "limit": candLimit}}
	}
	add := func(tool string) {
		switch tool {
		case "vecgrep":
			steps = append(steps, search("vecgrep"))
		case "codemap":
			// If the question looks like a symbol, resolve impact; else search by name.
			if isSymbolish(question) {
				steps = append(steps, step{tool: "codemap", op: "impact", input: map[string]any{"symbol": symbolToken(question)}})
			} else {
				steps = append(steps, step{tool: "codemap", op: "find", input: map[string]any{"query": question, "top": candLimit}})
			}
		case "cairntrace", "glyphrun":
			// Discovery-time cairntrace/glyphrun only surface prior context via
			// search; an actual run needs a spec (that happens at verify).
			steps = append(steps, search("vecgrep"))
		case "fcheap":
			steps = append(steps, step{tool: "fcheap", op: "search", input: map[string]any{"query": question, "limit": candLimit}})
		case "vidtrace":
			// Discovery: surface prior bug-video bundles. A specific bundle is
			// investigated via the explicit --video path.
			steps = append(steps, step{tool: "vidtrace", op: "stash_list", input: map[string]any{}})
		case "tvault":
			steps = append(steps, step{tool: "tvault", op: "availability", input: map[string]any{}})
		}
	}
	add(r.First)
	if r.FollowUp != r.First {
		add(r.FollowUp)
	}
	if len(steps) == 0 {
		steps = append(steps, search("vecgrep"))
	}
	return steps
}

func isSymbolish(q string) bool {
	return domain.RouteFor(q, nil).First == "codemap" || symbolShape(q)
}

func symbolShape(q string) bool {
	for i, r := range q {
		if i > 0 && r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return len(q) > 0 && (containsAnyRune(q, ".:/"))
}

func symbolToken(q string) string {
	// Take the last whitespace-delimited token that looks structural.
	fields := splitWS(q)
	for i := len(fields) - 1; i >= 0; i-- {
		if symbolShape(fields[i]) {
			return fields[i]
		}
	}
	if len(fields) > 0 {
		return fields[len(fields)-1]
	}
	return q
}

func containsAnyRune(s, set string) bool {
	for _, r := range s {
		for _, c := range set {
			if r == c {
				return true
			}
		}
	}
	return false
}

func splitWS(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// videoBundle returns the video reference when it looks like a bundle path (has
// a path separator or a file extension), else "". A vidtrace:// stash reference
// is NOT a bundle (its "//" would otherwise match the separator heuristic) — it
// is handled by videoStash.
func videoBundle(v string) string {
	if _, isStashURI := trimPrefix(v, "vidtrace://"); isStashURI {
		return ""
	}
	if containsAnyRune(v, "/\\") || (len(v) > 4 && containsAnyRune(v[len(v)-5:], ".")) {
		return v
	}
	return ""
}

// videoStash returns the vidtrace stash id when the reference is a bare id (not
// a path), stripping any vidtrace:// prefix.
func videoStash(v string) string {
	if s, ok := trimPrefix(v, "vidtrace://"); ok {
		return s
	}
	if videoBundle(v) == "" {
		return v
	}
	return ""
}

// rawVideoExts are extensions vidtrace can only consume after `vidtrace
// extract` — handed straight to `vidtrace investigate` they fail bundle
// validation with no indication that a raw file (not a bundle) was the
// problem.
var rawVideoExts = []string{".mp4", ".mov", ".mkv", ".webm", ".avi", ".m4v"}

// looksLikeRawVideo reports whether v is a path (not a bare stash id) ending
// in a known raw-video extension.
func looksLikeRawVideo(v string) bool {
	if videoBundle(v) == "" {
		return false
	}
	lower := strings.ToLower(v)
	for _, ext := range rawVideoExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func trimPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return s, false
}

func pluralizeEv(n int) string {
	if n == 1 {
		return "1 evidence item"
	}
	return fmt.Sprintf("%d evidence items", n)
}

func dedupeStr(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
