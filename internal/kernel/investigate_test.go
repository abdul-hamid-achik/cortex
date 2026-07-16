package kernel

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
)

// compoundQuestion is the real multi-part question from the 2026-07-15
// dogfooding session that was sent to vecgrep as one giant query.
const compoundQuestion = "How does data flow through Cartographer: where is deterministic fixture data created (createDemoDashboardData), how do dashboard routes/APIs consume it, how is campaign session state validated, how does the jobs queue-ingress boundary enforce idempotency/size limits/kill switch?"

func TestSubQuestionsDecomposesCompoundQuestion(t *testing.T) {
	subs := subQuestions(compoundQuestion, maxSubQueries)
	if len(subs) < 4 || len(subs) > maxSubQueries {
		t.Fatalf("expected 4..%d sub-queries, got %d: %v", maxSubQueries, len(subs), subs)
	}
	for _, want := range []string{
		"campaign session state validated",
		"deterministic fixture data created",
		"queue-ingress boundary",
	} {
		found := false
		for _, s := range subs {
			if strings.Contains(s, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no sub-query targets %q: %v", want, subs)
		}
	}
	for _, s := range subs {
		if s == compoundQuestion {
			t.Errorf("a sub-query must be narrower than the original question: %q", s)
		}
	}
}

func TestSubQuestionsPreservesCodeTokens(t *testing.T) {
	// ?, ;, : split only at clause boundaries (followed by whitespace/end).
	// "std::sort", URLs, and ternaries must not be shredded into garbage
	// sub-queries.
	subs := subQuestions("How does std::sort compare elements, and where is the comparator validated?", maxSubQueries)
	if len(subs) != 2 {
		t.Fatalf("expected 2 sub-queries, got %v", subs)
	}
	if !strings.Contains(subs[0], "std::sort") {
		t.Errorf("std::sort must survive decomposition intact, got %q", subs[0])
	}

	subs = subQuestions("why does https://example.com/callback return 404, and how is the redirect handled?", maxSubQueries)
	if len(subs) != 2 || !strings.Contains(subs[0], "https://example.com/callback") {
		t.Errorf("URL must survive decomposition intact, got %v", subs)
	}

	// A ternary's "? " boundary yields fragments too short to stand alone —
	// the question must not decompose at all.
	if subs := subQuestions("is `x ? y : z` evaluated lazily", maxSubQueries); subs != nil {
		t.Errorf("ternary question should not decompose, got %v", subs)
	}
}

func TestSubQuestionsLeavesSimpleQuestionAlone(t *testing.T) {
	for _, q := range []string{
		"where is the login redirect handled",
		"HandleCallback",
		"something vague to search",
	} {
		if subs := subQuestions(q, maxSubQueries); subs != nil {
			t.Errorf("simple question %q should not decompose, got %v", q, subs)
		}
	}
}

func TestInvestigateDeepDecomposesCompoundQuestion(t *testing.T) {
	// Deep mode splits a compound question into targeted discovery searches
	// instead of one giant embedding query (dogfooding 2026-07-15).
	vecgrep := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, testRepo(t), vecgrep, codemap)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	if inv, _ := k.Investigate(context.Background(), InvestigateInput{
		TaskID: env.TaskID, Question: compoundQuestion, Depth: "deep"}); !inv.OK {
		t.Fatalf("investigate failed: %s", inv.Error)
	}
	var queries []string
	for _, r := range vecgrep.requests() {
		if r.Operation == "search" {
			queries = append(queries, r.Str("query"))
		}
	}
	if len(queries) < 4 {
		t.Fatalf("deep mode should fan a compound question into multiple searches, got %d: %v", len(queries), queries)
	}
	for _, q := range queries {
		if q == compoundQuestion {
			t.Errorf("deep search should use a decomposed sub-query, not the full question: %q", q)
		}
	}
}

func TestInvestigateStandardKeepsSingleSearch(t *testing.T) {
	// Standard depth is unchanged: one discovery search with the full question.
	vecgrep := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative}}
	k := newTestKernel(t, testRepo(t), vecgrep, codemap)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	_, _ = k.Investigate(context.Background(), InvestigateInput{
		TaskID: env.TaskID, Question: compoundQuestion, Depth: "standard"})
	var queries []string
	for _, r := range vecgrep.requests() {
		if r.Operation == "search" {
			queries = append(queries, r.Str("query"))
		}
	}
	if len(queries) != 1 || queries[0] != compoundQuestion {
		t.Errorf("standard depth should run one search with the full question, got %v", queries)
	}
}

func TestInvestigateSurfacesEmptyStructuralStage(t *testing.T) {
	// When the codemap structural stage runs and resolves nothing, the summary
	// and warnings must say so instead of implying vecgrep→codemap succeeded
	// (dogfooding 2026-07-15: the summary claimed the full route while every
	// recorded fact was a vecgrep doc-header hit).
	vecgrep := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative},
		byOp: map[string]adapters.Result{
			"search": {Status: adapters.StatusAuthoritative, Facts: []adapters.Fact{
				{Kind: "semantic_search", Confidence: "low", Claim: "candidate",
					Location: &adapters.Location{File: "src/auth.go", Symbol: "HandleCallback"}},
			}},
		}}
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative}} // no structural facts
	k := newTestKernel(t, testRepo(t), vecgrep, codemap)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	inv, _ := k.Investigate(context.Background(), InvestigateInput{
		TaskID: env.TaskID, Question: "where is the login redirect handled"})
	if !strings.Contains(inv.Summary, "structural stage (codemap) returned no results") {
		t.Errorf("summary should surface the empty structural stage, got: %s", inv.Summary)
	}
	if !hasWarning(inv.Warnings, "structural stage (codemap) returned no results") {
		t.Errorf("warnings should surface the empty structural stage, got: %v", inv.Warnings)
	}
}

func TestInvestigateUnavailableStructuralStageStillSurfaced(t *testing.T) {
	// codemap resolving nothing via an unavailable/not_found result emits a
	// tool_unavailable fact — that record must NOT count as a structural result
	// and suppress the empty-stage note (found in e2e verification 2026-07-15:
	// the summary claimed "via vecgrep→codemap: 1 evidence item recorded" while
	// the only codemap item was the tool_unavailable record).
	vecgrep := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative},
		byOp: map[string]adapters.Result{
			"search": {Status: adapters.StatusAuthoritative, Facts: []adapters.Fact{
				{Kind: "semantic_search", Confidence: "low", Claim: "candidate",
					Location: &adapters.Location{File: "src/auth.go", Symbol: "HandleCallback"}},
			}},
		}}
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusUnavailable, Facts: []adapters.Fact{
			{Kind: "tool_unavailable", Confidence: "unknown", Claim: "codemap is unavailable (not_found)"},
		}}}
	k := newTestKernel(t, testRepo(t), vecgrep, codemap)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	inv, _ := k.Investigate(context.Background(), InvestigateInput{
		TaskID: env.TaskID, Question: "where is the login redirect handled"})
	if !strings.Contains(inv.Summary, "structural stage (codemap) returned no results") {
		t.Errorf("a tool_unavailable record must not suppress the empty structural stage note, got: %s", inv.Summary)
	}
}

func TestInvestigateStructuralResultsSuppressEmptyNote(t *testing.T) {
	// A structural stage that produced facts must NOT be reported as empty.
	vecgrep := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative},
		byOp: map[string]adapters.Result{
			"search": {Status: adapters.StatusAuthoritative, Facts: []adapters.Fact{
				{Kind: "semantic_search", Confidence: "low", Claim: "candidate",
					Location: &adapters.Location{File: "src/auth.go", Symbol: "HandleCallback"}},
			}},
		}}
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative},
		byOp: map[string]adapters.Result{
			"impact": {Status: adapters.StatusAuthoritative, Facts: []adapters.Fact{
				{Kind: "code_graph", Confidence: "medium", Claim: "HandleCallback blast radius: 3 callers"},
			}},
		}}
	k := newTestKernel(t, testRepo(t), vecgrep, codemap)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	inv, _ := k.Investigate(context.Background(), InvestigateInput{
		TaskID: env.TaskID, Question: "where is the login redirect handled"})
	if strings.Contains(inv.Summary, "returned no results") {
		t.Errorf("a productive structural stage must not be reported empty: %s", inv.Summary)
	}
}

func TestDecomposeSearchStepsPassesThroughNonSearch(t *testing.T) {
	steps := []step{
		{tool: "vecgrep", op: "search", input: map[string]any{"query": compoundQuestion, "limit": 8}},
		{tool: "vidtrace", op: "stash_list", input: map[string]any{}},
	}
	out := decomposeSearchSteps(steps, compoundQuestion, 8)
	searches, other := 0, 0
	for _, s := range out {
		if s.op == "search" {
			searches++
		} else {
			other++
		}
	}
	if searches < 4 {
		t.Errorf("expected the search step to fan out, got %d searches", searches)
	}
	if other != 1 {
		t.Errorf("non-search steps must pass through unchanged, got %d", other)
	}
	// A non-compound question leaves steps untouched.
	same := decomposeSearchSteps(steps, "where is the login redirect handled", 8)
	if len(same) != len(steps) {
		t.Errorf("simple question should leave steps untouched, got %d steps", len(same))
	}
}

func TestSubQuestionsSplitsObjectConjunction(t *testing.T) {
	// "enforce idempotency and size limits" conjoins two OBJECTS — no
	// interrogative follows the " and ", so the clause-boundary pass alone
	// left this question whole and discovery ran one averaged-out query that
	// returned doc mush (dogfooding 2026-07-16 against cartographer).
	subs := subQuestions("How does the jobs queue ingress enforce idempotency and size limits?", maxSubQueries)
	if len(subs) != 2 {
		t.Fatalf("expected 2 sub-queries, got %v", subs)
	}
	if !strings.Contains(subs[0], "idempotency") || strings.Contains(subs[0], "size limits") {
		t.Errorf("first sub-query should target idempotency only: %q", subs[0])
	}
	if !strings.Contains(subs[1], "size limits") || strings.Contains(subs[1], "idempotency") {
		t.Errorf("second sub-query should target size limits only: %q", subs[1])
	}
	for _, s := range subs {
		if !strings.Contains(s, "jobs queue ingress") {
			t.Errorf("sub-queries must keep the shared clause context: %q", s)
		}
	}
}

func TestSubQuestionsIgnoresShortConjunctions(t *testing.T) {
	// Conjunctions without enough shared context stay whole — splitting
	// "drag and drop" produces garbage queries.
	for _, q := range []string{
		"where is drag and drop handled",           // left side too short
		"does the export endpoint accept json and", // right side empty
		"how does the retry loop use jitter and b",  // right side one word
	} {
		if subs := subQuestions(q, maxSubQueries); subs != nil {
			t.Errorf("%q should not decompose, got %v", q, subs)
		}
	}
}

func TestInvestigateDeepDiscoveryDoesNotStarveStructuralStage(t *testing.T) {
	// Deep discovery returning a full budget of hits must still leave room
	// for the structural stage (dogfooding 2026-07-16: 16/16 recorded facts
	// were vecgrep hits, codemap never ran, and — because the stage was never
	// "attempted" — the empty-structural-stage warning stayed silent too).
	many := make([]adapters.Fact, 0, 40)
	for i := 0; i < 40; i++ {
		many = append(many, adapters.Fact{Kind: "semantic_search", Confidence: "low",
			Claim:    fmt.Sprintf("candidate %d", i),
			Location: &adapters.Location{File: "src/callback.go", Symbol: "HandleCallback"}})
	}
	vecgrep := &fakeAdapter{name: "vecgrep", caps: []adapters.Capability{adapters.CapabilityDiscover},
		result: adapters.Result{Status: adapters.StatusAuthoritative},
		byOp: map[string]adapters.Result{
			"search": {Status: adapters.StatusAuthoritative, Facts: many},
		}}
	codemap := &fakeAdapter{name: "codemap", caps: []adapters.Capability{adapters.CapabilityStructure},
		result: adapters.Result{Status: adapters.StatusAuthoritative},
		byOp: map[string]adapters.Result{
			"impact": {Status: adapters.StatusAuthoritative, Facts: []adapters.Fact{
				{Kind: "code_graph", Confidence: "medium", Claim: "HandleCallback blast radius: 3 callers"},
			}},
		}}
	k := newTestKernel(t, testRepo(t), vecgrep, codemap)
	env, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	inv, _ := k.Investigate(context.Background(), InvestigateInput{
		TaskID: env.TaskID, Question: "where is the login redirect handled", Depth: "deep"})
	if !inv.OK {
		t.Fatalf("investigate failed: %s", inv.Error)
	}
	if len(codemap.requests()) == 0 {
		t.Fatalf("structural stage must run even when discovery fills the budget; codemap got no requests")
	}
	if !strings.Contains(inv.Summary, "expanded structurally") {
		t.Errorf("summary should record the structural expansion, got: %s", inv.Summary)
	}
	if strings.Contains(inv.Summary, "returned no results") {
		t.Errorf("a productive structural stage must not be reported empty: %s", inv.Summary)
	}
}
