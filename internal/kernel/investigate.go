package kernel

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// InvestigateInput parameterizes Investigate.
type InvestigateInput struct {
	TaskID   string
	Question string
	Surfaces []domain.Surface
	Depth    string // quick | standard | deep
	// Video, when set, is a bug-video bundle path or vidtrace stash id: Cortex
	// runs vidtrace to turn the recording into timestamped evidence and link the
	// visible failure to code.
	Video string
}

// Investigate routes a question through the appropriate discovery and
// structural tools, records the returned evidence, and returns a
// bounded investigation summary. Search results are recorded as candidates, not
// proof.
func (k *Kernel) Investigate(ctx context.Context, in InvestigateInput) (domain.Envelope, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	in.Question = strings.TrimSpace(in.Question)
	if in.Question == "" {
		return errEnvelope(in.TaskID, "investigate needs a question"), nil
	}
	if c.Status != domain.PhaseInvestigating && c.Status != domain.PhasePlanned {
		return errEnvelope(in.TaskID, fmt.Sprintf("cannot investigate in phase %q; investigate happens after start", c.Status)), nil
	}
	depth, err := normalizeDepth(in.Depth)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	if len(in.Surfaces) > 0 {
		in.Surfaces, err = normalizeSurfaces(in.Surfaces)
		if err != nil {
			return errEnvelope(in.TaskID, err.Error()), nil
		}
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
	candLimit := depthCandidateLimit(depth, k.cfg.Budget.MaxCandidateFilesReturned)

	var facts []domain.Evidence
	var warnings []string
	degraded := false
	budget := k.cfg.Budget.MaxEvidenceItemsReturned
	if depth == "deep" {
		// Deep may return more hits per tool; still bound total evidence by budget.
		budget = k.cfg.Budget.MaxEvidenceItemsReturned
		if budget < candLimit {
			budget = candLimit
		}
	}

	// Count this round against the investigation budget. Exceeding it
	// is allowed but recorded — the point is to discourage frantic search, not to
	// hard-block a legitimately deep investigation.
	c.InvestigationRounds++
	if maxRounds := k.cfg.Budget.MaxInvestigationRounds; maxRounds > 0 && c.InvestigationRounds > maxRounds {
		note := fmt.Sprintf("investigation round %d exceeds the budget of %d — consider forming a hypothesis and planning, or state why deeper investigation is needed", c.InvestigationRounds, maxRounds)
		warnings = append(warnings, note)
		c.Notes = append(c.Notes, "budget: "+note)
	}

	// Causal routing runs discovery (vecgrep/vidtrace) first; the
	// top deduplicated file/symbol candidates are then fed into codemap as a
	// second structural stage, recording derivedFrom provenance on the
	// structural evidence (symptom → candidate → structural expansion). An
	// explicit bug-video bundle runs a vidtrace investigation up front, linking
	// the visible failure to code before discovery.
	//
	// Stage 1 builds the discovery steps. When codemap is the structural
	// follow-up of a discovery-first route, its step is deferred to stage 2 so
	// codemap receives candidates, not the raw question. A codemap-first route
	// (known symbol) or a non-codemap follow-up runs the full route in stage 1.
	discoveryRoute := route
	if route.FollowUp == "codemap" && route.First != "codemap" {
		discoveryRoute = domain.Route{First: route.First, FollowUp: route.First, Why: route.Why}
	}
	steps := routeSteps(discoveryRoute, in.Question, surfaces, candLimit)
	if depth == "quick" {
		// Quick: primary route tool only (no follow-up), still after memory recall.
		steps = firstRouteStep(steps, route)
	}
	if depth == "deep" {
		// Deep: a compound question ("where is X created, how is Y validated,
		// how does Z enforce…") is decomposed into targeted sub-queries — one
		// giant embedding query averages every clause into mush and returns
		// doc-header noise (dogfooding 2026-07-15).
		var subsUsed []string
		steps, subsUsed = decomposeSearchSteps(steps, in.Question, candLimit)
		if len(subsUsed) > 0 {
			// Surface the decomposition so the caller can see (and judge) the
			// actual queries that ran — an invisible split is indistinguishable
			// from no split. Gated on subsUsed, not subQuestions: a route with
			// no search step decomposes nothing, and claiming "sub-queries
			// searched" there would be a lie (panel review 2026-07-16).
			warnings = append(warnings, fmt.Sprintf(
				"deep decomposition: %d targeted sub-queries searched: %s",
				len(subsUsed), strings.Join(subsUsed, " | ")))
		}
	}
	// Recall prior durable conclusions for THIS repo first (semantic
	// recall): cortex writes a memory on every completed task, so a related past
	// case ("returnTo was dropped in HandleCallback…") becomes low-confidence
	// orientation instead of being re-derived. Scoped by the cortex+repo:<name>
	// tags the persist phase writes (never a bare repo name — "cortex" the
	// product must not match every project also named cortex).
	// Recall is orientation, not discovery: it stamps FIRST and therefore
	// eats the discovery budget before any search hit lands. At candLimit per
	// step the three recall steps could contribute 3×candLimit facts and
	// crowd every real search hit out of a reserved-down budget (panel review
	// 2026-07-16) — a few memories are plenty.
	recallLimit := 3
	if candLimit < recallLimit {
		recallLimit = candLimit
	}
	steps = append([]step{
		{tool: "vecgrep", op: "memory_recall", input: map[string]any{
			"query": in.Question, "tags": memoryTags(c), "limit": recallLimit,
		}},
		// Cross-case disproof recall includes prior rejected/challenged
		// hypotheses and definitive receipts, scoped to this repo. A second
		// unscoped step adds the cross-repo tier. Both are model_inference/low
		// orientation — candidatesFrom skips model_inference, so they never
		// become codemap candidates.
		{tool: "veclite", op: "case_recall", input: map[string]any{
			"query": in.Question, "repo": c.Workspace.Repository, "limit": recallLimit,
		}},
		{tool: "veclite", op: "case_recall", input: map[string]any{
			"query": in.Question, "repo": "", "limit": recallLimit,
		}},
	}, steps...)
	if in.Video != "" {
		steps = append([]step{{tool: "vidtrace", op: "investigate", input: map[string]any{
			"query": in.Question, "bundle": videoBundle(in.Video), "stash": videoStash(in.Video),
			"connect": true, "limit": candLimit,
		}}}, steps...)
	}
	// Stage 1 must not saturate the whole evidence budget when a structural
	// follow-up is deferred to stage 2: a full budget makes the
	// `len(facts) < budget` gate below silently cancel the codemap stage while
	// the summary still claims "via vecgrep→codemap" (dogfooding 2026-07-16: a
	// deep investigation recorded 16/16 discovery hits and structure never
	// ran). Reserve a slice of the budget for structural facts.
	discoveryBudget := budget
	if route.FollowUp == "codemap" && route.First != "codemap" && expansionLimit(depth, candLimit) > 0 {
		reserve := budget / 4
		if reserve < 2 {
			reserve = 2
		}
		if reserve > budget-1 {
			// A degenerate budget (≤ reserve) has no room to hold both stages;
			// clamping discovery to budget-1 keeps the stage-2 gate reachable
			// instead of silently re-cancelling the structural stage (panel
			// review 2026-07-16: reserve collapse at budget=1 reintroduced the
			// exact bug this reservation exists to fix).
			reserve = budget - 1
		}
		if reserve > 0 {
			discoveryBudget = budget - reserve
		}
	}
	// Execute stage 1 with bounded parallelism.
	// The steps are independent adapter calls — step N's result does not feed
	// step N+1's input here — so they fan out; evidence stamping runs
	// sequentially after, serializing store writes.
	results := k.runStepsParallel(ctx, c.ID, steps)
	var sErr error
	facts, warnings, degraded, sErr = k.stampResults(c, results, discoveryBudget, discoveryStageLabel(discoveryBudget, budget), facts, warnings, nil)
	if sErr != nil {
		return errEnvelope(c.ID, sErr.Error()), sErr
	}

	// Stage 2: structural expansion. Only when codemap is the deferred
	// structural follow-up of a discovery-first route, depth allows it, and the
	// evidence budget is not yet exhausted. Discovery candidates are deduplicated
	// and fed into codemap; each structural fact records derivedFrom provenance
	// linking it back to the discovery candidate that produced it. When
	// discovery yields no locatable candidates, the raw question falls through
	// to codemap exactly as the previous parallel route did (byte-identical).
	expanded := 0
	structuralAttempted := false
	structuralFacts := 0
	if route.FollowUp == "codemap" && route.First != "codemap" {
		if lim := expansionLimit(depth, candLimit); lim > 0 && len(facts) < budget {
			structuralAttempted = true
			before := len(facts)
			cands := candidatesFrom(facts, lim)
			if len(cands) > 0 {
				steps2, evIDs := structuralSteps(cands, candLimit)
				if len(steps2) > 0 {
					res2 := k.runStepsParallel(ctx, c.ID, steps2)
					var d2 bool
					facts, warnings, d2, sErr = k.stampResults(c, res2, budget, "budget", facts, warnings, func(i int) []string {
						if i < len(evIDs) {
							return []string{evIDs[i]}
						}
						return nil
					})
					if sErr != nil {
						return errEnvelope(c.ID, sErr.Error()), sErr
					}
					degraded = degraded || d2
					// Count the steps that actually ran, not the candidate pool —
					// skipped candidates are not expansions (audit 2026-07-16).
					expanded = len(steps2)
				}
			} else {
				// Fallback: no locatable candidates — feed the raw question to
				// codemap exactly as the previous parallel route did.
				var fb []step
				if isSymbolish(in.Question) {
					fb = []step{{tool: "codemap", op: "impact", input: map[string]any{"symbol": symbolToken(in.Question)}}}
				} else {
					fb = []step{{tool: "codemap", op: "find", input: map[string]any{"query": in.Question, "top": candLimit}}}
				}
				res2 := k.runStepsParallel(ctx, c.ID, fb)
				var d2 bool
				facts, warnings, d2, sErr = k.stampResults(c, res2, budget, "budget", facts, warnings, nil)
				if sErr != nil {
					return errEnvelope(c.ID, sErr.Error()), sErr
				}
				degraded = degraded || d2
			}
			// Count only substantive structural facts: a tool_unavailable record
			// ("no symbol named X", codemap missing) means the stage resolved
			// nothing — counting it would suppress the empty-stage note in the
			// exact situation it exists for (e2e verification 2026-07-15).
			for _, ev := range facts[before:] {
				if ev.Kind != domain.KindToolUnavailable {
					structuralFacts++
				}
			}
		}
	}

	if err := k.store.Save(c); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}

	summary := fmt.Sprintf("investigated %q via %s→%s: %s recorded (%s)",
		clipStr(in.Question, 60), route.First, route.FollowUp, pluralizeEv(len(facts)), route.Why)
	if expanded > 0 {
		// Stage 2 expanded discovery candidates into structural evidence;
		// surface the causal-routing provenance count.
		summary = fmt.Sprintf("investigated %q via %s→%s: %s recorded; %d discovery candidate(s) expanded structurally (%s)",
			clipStr(in.Question, 60), route.First, route.FollowUp, pluralizeEv(len(facts)), expanded, route.Why)
	}
	if structuralAttempted && structuralFacts == 0 {
		// The structural stage ran but resolved nothing — say so instead of
		// letting "via vecgrep→codemap" imply the expansion succeeded
		// (dogfooding 2026-07-15: a summary claimed vecgrep→codemap while every
		// recorded fact was a vecgrep hit).
		summary += "; structural stage (codemap) returned no results"
		warnings = append(warnings, "structural stage (codemap) returned no results this round — structure was not resolved; the evidence below is discovery-only")
	}
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
// max_parallel_calls. Steps are independent adapter calls, so they
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

// stampResults stamps every fact of every result into durable evidence,
// honoring the evidence budget. stage names the truncation message's cap so a
// discovery cap reduced by the structural reserve is not mistaken for the
// configured budget (panel review 2026-07-16: "truncated to 12" matched no
// configured limit). derivedFor(i) supplies the causal-routing provenance
// links for results[i]'s facts (nil for discovery stage). It returns the
// accumulated evidence, warnings, a degraded flag (true when any result was
// non-authoritative), and a stamping error. Shared by both investigation
// stages so evidence ordering and budget handling stay identical.
func (k *Kernel) stampResults(c *domain.CaseFile, results []adapters.Result, budget int, stage string, facts []domain.Evidence, warnings []string, derivedFor func(i int) []string) ([]domain.Evidence, []string, bool, error) {
	degraded := false
	for ri, res := range results {
		// A non-authoritative result (partial/unavailable/error) means this
		// step's facts (if any) are stale, incomplete, or a fallback — never
		// silently as trustworthy as a clean search. Surfaced as a top-level
		// flag, not just a warning string (dogfooding 2026-07-07).
		if res.Status != adapters.StatusAuthoritative {
			degraded = true
		}
		warnings = append(warnings, res.Warnings...)
		rawRef := k.storeRaw(c.ID, res) // one stored blob per tool call, shared by its facts
		var links []string
		if derivedFor != nil {
			links = derivedFor(ri)
		}
		for _, f := range res.Facts {
			if len(facts) >= budget {
				warnings = append(warnings, fmt.Sprintf("evidence truncated to %d items (%s)", budget, stage))
				break
			}
			ev, err := k.stampEvidenceDerived(c.ID, res.Tool, f, rawRef, links)
			if err != nil {
				return facts, warnings, degraded, err
			}
			facts = append(facts, ev)
		}
	}
	return facts, warnings, degraded, nil
}

// normalizeDepth validates and canonicalizes quick | standard | deep. Unknown
// values are rejected rather than silently becoming standard, since that can
// launch more adapter work than the caller intended.
func normalizeDepth(d string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(d)) {
	case "":
		return "standard", nil
	case "quick":
		return "quick", nil
	case "standard":
		return "standard", nil
	case "deep":
		return "deep", nil
	default:
		return "", fmt.Errorf("investigation depth must be one of: quick, standard, deep (got %q)", d)
	}
}

// depthCandidateLimit scales the per-tool candidate budget by depth.
func depthCandidateLimit(depth string, base int) int {
	if base < 1 {
		base = 8
	}
	switch depth {
	case "quick":
		if base > 4 {
			return 4
		}
		return base
	case "deep":
		n := base * 2
		if n < 12 {
			n = 12
		}
		if n > 24 {
			return 24
		}
		return n
	default:
		return base
	}
}

// firstRouteStep keeps only the primary route tool's step(s) for a quick
// investigation (drops the structural follow-up when first ≠ follow-up).
func firstRouteStep(steps []step, r domain.Route) []step {
	if len(steps) == 0 {
		return steps
	}
	if r.FollowUp == "" || r.FollowUp == r.First {
		return steps
	}
	var out []step
	for _, s := range steps {
		if s.tool == r.First {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return steps[:1]
	}
	return out
}

// routeSteps expands a Route into concrete adapter operations for the question.
// Discovery searches are capped at candLimit candidate hits so one broad
// search can't flood the ledger.
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

// maxSubQueries bounds the deep-mode decomposition fan-out so one compound
// question cannot explode into unbounded discovery calls.
const maxSubQueries = 5

// interrogatives are the clause openers that mark a new sub-question after a
// comma or "and" ("…, how is session state validated, and where is …").
var interrogatives = map[string]bool{
	"how": true, "where": true, "what": true, "why": true, "when": true,
	"which": true, "who": true, "does": true, "do": true, "is": true,
	"are": true, "can": true,
}

// subQuestions decomposes a compound question into targeted sub-queries using
// a deliberately heuristic split (no LLM): hard separators (?, ;, :) first,
// then comma/"and" boundaries that introduce a new interrogative clause.
// Fragments under three words are dropped, duplicates collapse, and the result
// is capped at max. A question that does not decompose returns nil — callers
// keep the original question.
func subQuestions(q string, max int) []string {
	if max < 2 {
		return nil
	}
	const marker = "\x00"
	// Hard separators (?, ;, :) split only when followed by whitespace or the
	// end of the question — a ? / ; / : glued to the next character is code or
	// a URL ("std::sort", "https://…", "a?b:c"), not a clause boundary, and
	// splitting there mangles the question into garbage sub-queries.
	mb := make([]byte, 0, len(q))
	for i := 0; i < len(q); i++ {
		ch := q[i]
		if (ch == '?' || ch == ';' || ch == ':') &&
			(i+1 == len(q) || q[i+1] == ' ' || q[i+1] == '\t' || q[i+1] == '\n') {
			mb = append(mb, marker[0])
			continue
		}
		mb = append(mb, ch)
	}
	marked := string(mb)
	// Soft separators: a comma or " and " followed by an interrogative word.
	var b strings.Builder
	for i := 0; i < len(marked); {
		cut := 0
		for _, sep := range []string{", ", " and "} {
			if hasFoldPrefix(marked[i:], sep) && interrogatives[firstWordLower(marked[i+len(sep):])] {
				cut = len(sep)
				break
			}
		}
		if cut > 0 {
			b.WriteString(marker)
			i += cut
			continue
		}
		b.WriteByte(marked[i])
		i++
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(b.String(), marker) {
		p = strings.Trim(strings.TrimSpace(p), ",")
		p = strings.TrimSpace(p)
		if len(splitWS(p)) < 3 {
			continue // too short to stand alone as a query
		}
		// An object split emits the ORIGINAL fragment first, then the two
		// targeted conjunct queries. The original must survive: the split is
		// heuristic and sometimes fires on non-object conjunctions ("search
		// and replace"), and at the cap a partial emit would silently drop
		// the right conjunct's terms from the whole query set (panel review
		// 2026-07-16) — original-first means whatever the cap keeps still
		// covers all the question's terms.
		parts := []string{p}
		parts = append(parts, objectConjunctSplit(p)...)
		for _, part := range parts {
			key := strings.ToLower(part)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, part)
			if len(out) == max {
				break
			}
		}
		if len(out) == max {
			break
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// lastIndexFoldASCII returns the last index of sub in s comparing ASCII
// case-insensitively over the ORIGINAL bytes. strings.LastIndex over a
// ToLower copy is not equivalent: Unicode case mapping changes UTF-8 byte
// length for some runes (Ⱥ, İ, the Kelvin sign), so an index computed in the
// lowered copy panics or splits mid-rune when applied to the original (panel
// review 2026-07-16, reproduced). sub must be ASCII.
func lastIndexFoldASCII(s, sub string) int {
	for i := len(s) - len(sub); i >= 0; i-- {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// objectConjunctSplit splits a clause whose tail conjoins two parallel objects
// ("… enforce idempotency and size limits") into one query per object. The
// clause-boundary pass above cannot see these: the conjunct after " and "
// opens with a noun, not an interrogative, so the question reaches the
// embedder as one averaged-out query (dogfooding 2026-07-16: "how does the
// jobs queue ingress enforce idempotency and size limits?" did not decompose
// and discovery returned doc mush). Only the RIGHTMOST " and " is considered
// (object lists sit at a clause's end), the right conjunct must be a short
// phrase (2–3 words) that does not open a new interrogative clause, and the
// left side must be long enough (≥5 words) to carry the shared context. The
// second query drops the left side's last word — the first object's head —
// and grafts the right conjunct into its slot, keeping the verb ("…enforce
// idempotency and size limits" → "…enforce size limits"). The caller keeps
// the original clause alongside these, so a heuristic miss adds noise but
// never loses the real query. Returns nil when the shape doesn't hold.
func objectConjunctSplit(p string) []string {
	i := lastIndexFoldASCII(p, " and ")
	if i < 0 {
		return nil
	}
	left := strings.TrimSpace(p[:i])
	right := strings.TrimSpace(p[i+len(" and "):])
	lw, rw := splitWS(left), splitWS(right)
	if len(lw) < 5 || len(rw) < 2 || len(rw) > 3 || interrogatives[strings.ToLower(rw[0])] {
		return nil
	}
	second := strings.Join(append(append([]string{}, lw[:len(lw)-1]...), rw...), " ")
	return []string{left, second}
}

// decomposeSearchSteps rewrites each discovery search step over a compound
// question into one targeted search per sub-question, returning the rewritten
// steps and the sub-queries that were actually installed into a search step.
// Non-search steps (codemap impact/find, artifact listings) pass through
// unchanged; a question that does not decompose — or a route with no search
// step at all — leaves the steps untouched and returns nil sub-queries, so
// callers can report decomposition only when it really happened. Each tool's
// search is expanded once even if the route named it twice.
func decomposeSearchSteps(steps []step, question string, candLimit int) ([]step, []string) {
	subs := subQuestions(question, maxSubQueries)
	if len(subs) == 0 {
		return steps, nil
	}
	expandedTools := map[string]bool{}
	var out []step
	for _, s := range steps {
		if s.op != "search" {
			out = append(out, s)
			continue
		}
		if expandedTools[s.tool] {
			continue
		}
		expandedTools[s.tool] = true
		for _, sub := range subs {
			out = append(out, step{tool: s.tool, op: "search", input: map[string]any{"query": sub, "limit": candLimit}})
		}
	}
	if len(expandedTools) == 0 {
		return steps, nil
	}
	return out, subs
}

// discoveryStageLabel names the cap stampResults reports when stage-1
// truncation fires. When the structural reserve shrank the discovery cap, the
// truncation count matches no configured limit — spell out where the number
// comes from instead of calling it "budget" (panel review 2026-07-16).
func discoveryStageLabel(discoveryBudget, budget int) string {
	if discoveryBudget < budget {
		return fmt.Sprintf("discovery cap: %d-item budget minus %d reserved for the structural stage", budget, budget-discoveryBudget)
	}
	return "budget"
}

// hasFoldPrefix reports whether s begins with prefix, ASCII case-insensitively.
func hasFoldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// firstWordLower returns the first whitespace-delimited word of s, lowercased.
func firstWordLower(s string) string {
	fields := splitWS(strings.TrimSpace(s))
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

// candidate is one deduplicated discovery hit selected for structural expansion
// (causal routing stage 2). Symbol is the preferred expansion key (codemap
// impact); File is the fallback (codemap find on the base name). EvidenceID
// links the structural fact back to the discovery evidence that produced it.
type candidate struct {
	Symbol     string
	File       string
	EvidenceID string
}

// candidatesFrom extracts up to max deduplicated file/symbol candidates from
// freshly stamped discovery evidence, preserving evidence order. Records
// without a location, tool_unavailable records, and model_inference records
// (memory recall) never become candidates. A symbol wins over a file-only
// candidate sharing its file: symbol candidates are collected first, then a
// file-only candidate whose file already backs a symbol candidate is suppressed.
func candidatesFrom(evs []domain.Evidence, max int) []candidate {
	if max <= 0 {
		return nil
	}
	var out []candidate
	seenSymbol := map[string]bool{}
	seenFile := map[string]bool{}
	symbolFiles := map[string]bool{} // files already backing a symbol candidate
	// First pass: symbol-bearing candidates (a symbol is the strongest
	// structural key — codemap impact resolves the blast radius directly).
	for _, ev := range evs {
		if len(out) >= max {
			break
		}
		if ev.Kind == domain.KindToolUnavailable || ev.Kind == domain.KindModelInference {
			continue
		}
		if ev.Location == nil || strings.TrimSpace(ev.Location.Symbol) == "" {
			continue
		}
		if ev.Location.File != "" && !isCodeFile(ev.Location.File) {
			// A "symbol" from a non-code hit is a markdown heading or a config
			// key — codemap impact on it can only return not_found noise and a
			// misleading degraded flag (audit 2026-07-16: README/.env/AGENTS.md
			// hits became `codemap impact <heading>` steps).
			continue
		}
		key := strings.ToLower(ev.Location.Symbol)
		if seenSymbol[key] {
			continue
		}
		seenSymbol[key] = true
		if ev.Location.File != "" {
			symbolFiles[ev.Location.File] = true
		}
		out = append(out, candidate{Symbol: ev.Location.Symbol, File: ev.Location.File, EvidenceID: ev.ID})
	}
	// Second pass: file-only candidates, suppressed when a symbol candidate
	// already covers that file (symbol wins).
	for _, ev := range evs {
		if len(out) >= max {
			break
		}
		if ev.Kind == domain.KindToolUnavailable || ev.Kind == domain.KindModelInference {
			continue
		}
		if ev.Location == nil || ev.Location.Symbol != "" {
			continue
		}
		f := strings.TrimSpace(ev.Location.File)
		if f == "" || !isCodeFile(f) {
			continue
		}
		if symbolFiles[f] || seenFile[f] {
			continue
		}
		seenFile[f] = true
		out = append(out, candidate{File: f, EvidenceID: ev.ID})
	}
	return out
}

// codeFileExts is the extension allowlist for structural expansion — the
// languages codemap can actually resolve. Discovery hits in docs, configs,
// or data files are real evidence but useless as codemap inputs.
var codeFileExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".mjs": true, ".cjs": true, ".py": true, ".rs": true, ".java": true,
	".rb": true, ".lua": true, ".vue": true, ".c": true, ".h": true,
	".cc": true, ".cpp": true, ".hpp": true, ".cs": true, ".kt": true,
	".swift": true, ".php": true, ".scala": true,
}

// isCodeFile reports whether a discovery hit's file can meaningfully feed the
// codemap structural stage.
func isCodeFile(path string) bool {
	dot := strings.LastIndex(path, ".")
	if dot < 0 {
		return false
	}
	return codeFileExts[strings.ToLower(path[dot:])]
}

// structuralSteps builds the stage-2 codemap steps and, in lockstep, the
// EvidenceID of the discovery candidate behind each step. Steps and links are
// appended in the same loop iteration so a skipped candidate (a dotfile whose
// query token is empty) can never desynchronize them — indexing the candidate
// slice by result position misattributed derivedFrom provenance for every
// fact after a skip (audit 2026-07-16: silent corruption of the audit trail).
func structuralSteps(cands []candidate, candLimit int) ([]step, []string) {
	if candLimit < 1 {
		candLimit = 8
	}
	var steps []step
	var evIDs []string
	for _, c := range cands {
		s := strings.TrimSpace(c.Symbol)
		f := strings.TrimSpace(c.File)
		if s == "" && f == "" {
			continue
		}
		if s != "" {
			steps = append(steps, step{tool: "codemap", op: "impact", input: map[string]any{"symbol": s}})
		} else {
			tok := fileQueryToken(f)
			if tok == "" {
				continue
			}
			steps = append(steps, step{tool: "codemap", op: "find", input: map[string]any{"query": tok, "top": candLimit}})
		}
		evIDs = append(evIDs, c.EvidenceID)
	}
	return steps, evIDs
}

// expansionLimit bounds how many discovery candidates feed codemap per round:
// standard → min(3, candLimit); deep → min(6, candLimit); quick → 0 (no
// structural expansion — quick stays discovery-only).
func expansionLimit(depth string, candLimit int) int {
	switch depth {
	case "deep":
		n := 6
		if candLimit < n {
			n = candLimit
		}
		return n
	case "quick":
		return 0
	default:
		n := 3
		if candLimit < n {
			n = candLimit
		}
		return n
	}
}

// fileQueryToken reduces "src/auth/callback.go" → "callback" for codemap find:
// the directory path and extension are stripped, leaving the base name. A bare
// name like "README" is returned unchanged; "" stays "".
func fileQueryToken(path string) string {
	if path == "" {
		return ""
	}
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[:i]
	}
	return base
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
