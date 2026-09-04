package kernel

import (
	"context"
	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"testing"
)

func TestStructuralCandidatesKeepHomonymsAndBatchExactTargets(t *testing.T) {
	evidence := []domain.Evidence{
		{ID: "a", Kind: domain.KindSemanticSearch, Location: &domain.Location{File: "a.go", StartLine: 3, Symbol: "Run", FQN: "p.A.Run", Kind: "method"}},
		{ID: "b", Kind: domain.KindSemanticSearch, Location: &domain.Location{File: "b.go", StartLine: 3, Symbol: "Run", FQN: "p.B.Run", Kind: "method"}},
	}
	candidates := candidatesFrom(evidence, 5)
	if len(candidates) != 2 {
		t.Fatalf("homonyms collapsed: %+v", candidates)
	}
	steps, origins := structuralSteps(candidates, 5)
	batch := batchStructuralSteps(steps)
	if len(batch) != 1 {
		t.Fatalf("steps=%+v", batch)
	}
	targets := batch[0].input["targets"].([]adapters.Request)
	if len(targets) != 2 || targets[0].Str("file") != "a.go" || targets[1].Str("fqn") != "p.B.Run" || origins[1] != "b" {
		t.Fatalf("targets=%+v origins=%v", targets, origins)
	}
	if semanticDiscoveryUnavailable([]step{{tool: "vecgrep", op: "search"}}, []adapters.Result{{Status: adapters.StatusPartial, Freshness: "stale"}}) {
		t.Fatal("a stale index must not trigger a missing-index fallback")
	}
}

func TestBatchEvidenceRetainsEachDiscoveryParent(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, err := k.StartTask(context.Background(), StartInput{Goal: "trace two definitions"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := k.store.Load(started.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var parents []string
	var batch []adapters.Fact
	for i, file := range []string{"a.go", "b.go"} {
		parent, err := k.stampEvidenceDerived(c.ID, "vecgrep", adapters.Fact{Kind: "semantic_search", Claim: "candidate in " + file, Confidence: "low"}, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		parents = append(parents, parent.ID)
		input := i
		batch = append(batch, adapters.Fact{Kind: "code_graph", Claim: "impact for " + file, Confidence: "medium", InputIndex: &input})
	}
	facts, _, _, err := k.stampResults(c, []adapters.Result{{Tool: "codemap", Status: adapters.StatusAuthoritative, Facts: batch}}, 10, "structural", nil, nil, func(i int) []string { return []string{parents[i]} })
	if err != nil || len(facts) != 2 {
		t.Fatalf("facts=%+v err=%v", facts, err)
	}
	for i, fact := range facts {
		if len(fact.DerivedFrom) != 1 || fact.DerivedFrom[0] != parents[i] {
			t.Fatalf("wrong parent: %+v", fact)
		}
	}
}
