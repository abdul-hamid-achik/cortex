package mcp

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/contracttest"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

const mcpConformanceSizeBehavior = "JSON text and structured content carry the same bounded shared-envelope value"

func assertMCPFixture(t *testing.T, id, generatedBy string, payload any) {
	t.Helper()
	fixture, err := contracttest.NewFixture(
		id, generatedBy, "canonical", mcpConformanceSizeBehavior, payload, nil,
	)
	if err != nil {
		t.Fatalf("build fixture %s: %v", id, err)
	}
	if err := contracttest.AssertGolden(id, fixture); err != nil {
		t.Fatal(err)
	}
}

func TestPublicConformanceMCPSharedEnvelopeParity(t *testing.T) {
	tests := []struct {
		name string
		env  domain.Envelope
	}{
		{
			name: "mcp_shared_envelope_success",
			env: domain.Envelope{
				OK: true, TaskID: "task_contract_success", Phase: domain.PhaseInvestigating,
				Summary: "task opened", NextActions: []string{"investigate"},
			},
		},
		{
			name: "mcp_shared_envelope_rejection",
			env: domain.Envelope{
				OK: false, TaskID: "task_contract_rejection", Phase: domain.PhaseChanging,
				Summary: "change lease belongs to another actor", Error: "change lease belongs to another actor",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, structured, err := result(tt.env, nil)
			if err != nil {
				t.Fatal(err)
			}
			var textValue any
			if err := json.Unmarshal([]byte(textOf(result)), &textValue); err != nil {
				t.Fatalf("decode JSON text: %v", err)
			}
			structuredJSON, err := json.Marshal(structured)
			if err != nil {
				t.Fatal(err)
			}
			var structuredValue any
			if err := json.Unmarshal(structuredJSON, &structuredValue); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(textValue, structuredValue) {
				t.Fatalf("JSON text and structured content differ: text=%v structured=%v", textValue, structuredValue)
			}
			if result.IsError != !tt.env.OK {
				t.Fatalf("isError=%t, want %t", result.IsError, !tt.env.OK)
			}
			assertMCPFixture(t, tt.name, "TestPublicConformanceMCPSharedEnvelopeParity/"+tt.name, map[string]any{
				"isError": result.IsError, "jsonText": textOf(result), "structuredContent": structuredValue,
			})
		})
	}
}
