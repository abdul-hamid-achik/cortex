package trajectory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
)

func validLauncherResult(t *testing.T) ([]byte, string) {
	t.Helper()
	request := LauncherRequest{SchemaVersion: 1, Arm: ArmRawTools}
	_, digest, err := encodeLauncherRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	result := LauncherResult{
		SchemaVersion: 1, RequestDigest: digest, Status: RunCompleted,
		ReportedCompletion: baseeval.CompletionUnverified,
		EffectiveModel:     Model{Identifier: "model", Build: "build", Temperature: 0, Seed: int64Pointer(1), ContextBudgetTokens: 1},
		Toolchain:          testToolchain(t, "go"),
		SelectedVerifiers:  []string{"go_tests"},
		Receipts: []ReceiptObservation{{
			ClaimIDs: []string{"terminal_help"}, VerifierID: "go_tests", Status: "not_run",
		}},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return data, digest
}

func TestLauncherResultStrictSchemaAndDigest(t *testing.T) {
	data, digest := validLauncherResult(t)
	tests := []struct {
		name string
		data string
		want string
	}{
		{"valid", string(data), ""},
		{"unknown field", strings.TrimSuffix(string(data), "}") + `,"future":true}`, "unknown field"},
		{"multiple values", string(data) + `{}`, "multiple JSON values"},
		{"future schema", strings.Replace(string(data), `"schemaVersion":1`, `"schemaVersion":2`, 1), "unsupported launcher result schema"},
		{"wrong digest", strings.Replace(string(data), digest, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1), "does not match"},
		{"invalid status", strings.Replace(string(data), `"status":"completed"`, `"status":"passed"`, 1), "invalid launcher run status"},
		{"invalid receipt", strings.Replace(string(data), `"status":"not_run"`, `"status":"passed-ish"`, 1), "invalid receipt status"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeLauncherResult([]byte(test.data), digest)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
}

func TestLauncherResultMustMatchManifestBounds(t *testing.T) {
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	result := LauncherResult{
		SelectedVerifiers: []string{"unknown_verifier"},
		EffectiveModel:    manifest.Model,
		Observation:       LauncherObservation{},
		ToolCalls:         1,
		Toolchain:         testToolchain(t, "go"),
	}
	workspace := t.TempDir()
	if _, err := validateLauncherResultForManifest(result, manifest, ArmRawTools, workspace); err == nil || !strings.Contains(err.Error(), "not a scenario verification target") {
		t.Fatalf("unknown verifier accepted: %v", err)
	}
	result.SelectedVerifiers = nil
	result.Receipts = []ReceiptObservation{{ClaimIDs: []string{"unknown_claim"}, VerifierID: "go_tests", Status: "not_run"}}
	if _, err := validateLauncherResultForManifest(result, manifest, ArmRawTools, workspace); err == nil || !strings.Contains(err.Error(), "unknown acceptance criterion") {
		t.Fatalf("unknown claim accepted: %v", err)
	}
	result.Receipts = nil
	result.ToolCalls = manifest.Budget.MaxToolCalls + 1
	if _, err := validateLauncherResultForManifest(result, manifest, ArmRawTools, workspace); err == nil || !strings.Contains(err.Error(), "exceeding") {
		t.Fatalf("tool budget overrun accepted: %v", err)
	}
	result.ToolCalls = 1
	result.EffectiveModel.Build = "different-build"
	if _, err := validateLauncherResultForManifest(result, manifest, ArmRawTools, workspace); err == nil || !strings.Contains(err.Error(), "effective model") {
		t.Fatalf("model mismatch accepted: %v", err)
	}
}

func TestReceiptVerifierMustCoverItsClaim(t *testing.T) {
	manifest := Manifest{
		Acceptance: []domain.AcceptanceCriterion{
			{ID: "code_claim", Statement: "code claim"},
			{ID: "terminal_claim", Statement: "terminal claim"},
		},
		Oracle: Oracle{Commands: []OracleCommand{
			{ID: "code_test", ClaimIDs: []string{"code_claim"}},
			{ID: "terminal_test", ClaimIDs: []string{"terminal_claim"}},
		}},
		Model:  Model{Identifier: "model", Build: "build", Temperature: 0, Seed: int64Pointer(1), ContextBudgetTokens: 1},
		Budget: Budget{MaxToolCalls: 10},
	}
	result := LauncherResult{
		EffectiveModel: manifest.Model, ToolCalls: 1,
		Toolchain: testToolchain(t, "go"),
		Receipts: []ReceiptObservation{{
			VerifierID: "code_test", ClaimIDs: []string{"terminal_claim"}, Status: "not_run",
		}},
	}
	if _, err := validateLauncherResultForManifest(result, manifest, ArmRawTools, t.TempDir()); err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("mismatched verifier/claim accepted: %v", err)
	}
}

func TestToolchainMustMatchArmAndExecutableDigest(t *testing.T) {
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	result := LauncherResult{
		EffectiveModel: manifest.Model,
		ToolCalls:      1,
		Toolchain:      testToolchain(t, "cortex"),
	}
	workspace := t.TempDir()
	if _, err := validateLauncherResultForManifest(result, manifest, ArmCortexBob, workspace); err == nil || !strings.Contains(err.Error(), "missing required executable \"bob\"") {
		t.Fatalf("missing Bob toolchain entry accepted: %v", err)
	}
	result.Toolchain = testToolchain(t, "cortex", "bob")
	result.Toolchain[0].BinaryDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := validateLauncherResultForManifest(result, manifest, ArmCortexBob, workspace); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched toolchain digest accepted: %v", err)
	}
	result.Toolchain = testToolchain(t, "cortex", "bob")
	if got, err := validateLauncherResultForManifest(result, manifest, ArmCortexBob, workspace); err != nil || len(got) != 2 {
		t.Fatalf("valid Cortex+Bob toolchain rejected: %+v (%v)", got, err)
	}
}

func testToolchain(t *testing.T, names ...string) []ToolchainProvenance {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := regularFileDigest(executable)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]ToolchainProvenance, 0, len(names))
	for _, name := range names {
		result = append(result, ToolchainProvenance{
			Name: name, Version: "test-build", ExecutablePath: executable, BinaryDigest: digest,
		})
	}
	return result
}

func int64Pointer(value int64) *int64 { return &value }
