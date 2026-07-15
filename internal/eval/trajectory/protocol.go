package trajectory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
)

const ProtocolSchemaVersion = 1

const maxLauncherConfigBytes = 64 << 10

type LauncherConfig struct {
	SchemaVersion int      `yaml:"schema_version" json:"schemaVersion"`
	Argv          []string `yaml:"argv" json:"argv"`
}

// LoadLauncherConfig reads trusted operator configuration kept separately
// from scenario YAML. It contains only exact argv and cannot inject env vars.
func LoadLauncherConfig(path string) (LauncherConfig, error) {
	data, err := readBoundedFile(path, maxLauncherConfigBytes)
	if err != nil {
		return LauncherConfig{}, err
	}
	var config LauncherConfig
	if err := decodeStrictYAML(data, &config); err != nil {
		return LauncherConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return LauncherConfig{}, err
	}
	return config, nil
}

func (c LauncherConfig) Validate() error {
	if c.SchemaVersion != ProtocolSchemaVersion {
		return fmt.Errorf("unsupported trajectory launcher schema version %d", c.SchemaVersion)
	}
	if len(c.Argv) == 0 || len(c.Argv) > 64 {
		return errors.New("launcher argv must contain between 1 and 64 entries")
	}
	if strings.TrimSpace(c.Argv[0]) == "" {
		return errors.New("launcher executable cannot be empty")
	}
	if !filepath.IsAbs(c.Argv[0]) || filepath.Clean(c.Argv[0]) != c.Argv[0] {
		return errors.New("launcher executable must be an absolute clean path")
	}
	for _, arg := range c.Argv {
		if strings.ContainsRune(arg, 0) || len(arg) > 4<<10 {
			return errors.New("launcher argv contains an invalid entry")
		}
	}
	return nil
}

type LauncherRequest struct {
	SchemaVersion       int                          `json:"schemaVersion"`
	Arm                 Arm                          `json:"arm"`
	Workspace           string                       `json:"workspace"`
	Goal                string                       `json:"goal"`
	Acceptance          []domain.AcceptanceCriterion `json:"acceptance"`
	Surfaces            []domain.Surface             `json:"surfaces"`
	VerificationTargets []VerificationTarget         `json:"verificationTargets"`
	Model               Model                        `json:"model"`
	Budget              Budget                       `json:"budget"`
}

// VerificationTarget identifies a verifier result the launcher may report.
// It intentionally excludes the oracle argv/path so the model cannot replace
// the runner's independent judgment with self-report.
type VerificationTarget struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	ClaimIDs []string `json:"claimIds"`
}

type RunStatus string

const (
	RunCompleted  RunStatus = "completed"
	RunFailed     RunStatus = "failed"
	RunIncomplete RunStatus = "incomplete"
	RunBlocked    RunStatus = "blocked"
	RunTimeout    RunStatus = "timeout"
)

type ReceiptObservation struct {
	ClaimIDs    []string `json:"claimIds"`
	VerifierID  string   `json:"verifierId"`
	Status      string   `json:"status"`
	Revision    string   `json:"revision,omitempty"`
	DirtyDigest string   `json:"dirtyDigest,omitempty"`
}

// LauncherResult is instrumented output from the trusted arm launcher. The
// runner independently derives oracle, diff, stale-proof, and honesty metrics.
type LauncherResult struct {
	SchemaVersion       int                      `json:"schemaVersion"`
	RequestDigest       string                   `json:"requestDigest"`
	Status              RunStatus                `json:"status"`
	ReportedCompletion  baseeval.CompletionLabel `json:"reportedCompletion"`
	EffectiveModel      Model                    `json:"effectiveModel"`
	Observation         LauncherObservation      `json:"observation"`
	Toolchain           []ToolchainProvenance    `json:"toolchain"`
	SelectedVerifiers   []string                 `json:"selectedVerifiers,omitempty"`
	Receipts            []ReceiptObservation     `json:"receipts,omitempty"`
	ToolCalls           int                      `json:"toolCalls"`
	InputTokens         *int64                   `json:"inputTokens,omitempty"`
	OutputTokens        *int64                   `json:"outputTokens,omitempty"`
	EstimatedCostMicros *int64                   `json:"estimatedCostMicros,omitempty"`
	HumanInterventions  int                      `json:"humanInterventions"`
}

// LauncherObservation contains only dimensions the trusted launcher can
// instrument. Scope, receipts, completion, latency, and cost are derived or
// validated by the trajectory runner instead of accepted from model output.
type LauncherObservation struct {
	Evidence         baseeval.EvidenceObservation `json:"evidence"`
	Disproof         baseeval.DisproofObservation `json:"disproof"`
	Recovery         baseeval.RecoveryObservation `json:"recovery"`
	BoundaryDeclared bool                         `json:"boundaryDeclared"`
}

// ToolchainProvenance identifies one executable that was actually exposed to
// an arm. The runner resolves and hashes ExecutablePath independently before
// accepting the launcher's attestation.
type ToolchainProvenance struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	ExecutablePath string `json:"executablePath"`
	BinaryDigest   string `json:"binaryDigest"`
}

func (r LauncherResult) Validate(expectedDigest string) error {
	if r.SchemaVersion != ProtocolSchemaVersion {
		return fmt.Errorf("unsupported launcher result schema version %d", r.SchemaVersion)
	}
	if r.RequestDigest != expectedDigest {
		return errors.New("launcher result request digest does not match the request")
	}
	switch r.Status {
	case RunCompleted, RunFailed, RunIncomplete, RunBlocked, RunTimeout:
	default:
		return fmt.Errorf("invalid launcher run status %q", r.Status)
	}
	switch r.ReportedCompletion {
	case baseeval.CompletionIncomplete, baseeval.CompletionVerified, baseeval.CompletionUnverified, baseeval.CompletionFailed:
	default:
		return fmt.Errorf("invalid reported completion %q", r.ReportedCompletion)
	}
	if r.HumanInterventions < 0 {
		return errors.New("human interventions cannot be negative")
	}
	if r.ToolCalls < 0 {
		return errors.New("tool calls cannot be negative")
	}
	if len(r.Toolchain) == 0 || len(r.Toolchain) > 64 {
		return errors.New("toolchain must contain between 1 and 64 executables")
	}
	seenTools := map[string]bool{}
	for _, tool := range r.Toolchain {
		if !stableIDPattern.MatchString(tool.Name) || seenTools[tool.Name] {
			return fmt.Errorf("invalid or duplicate toolchain name %q", tool.Name)
		}
		seenTools[tool.Name] = true
		if strings.TrimSpace(tool.Version) == "" || len(tool.Version) > 256 {
			return fmt.Errorf("toolchain %q version must be non-empty and at most 256 bytes", tool.Name)
		}
		if !filepath.IsAbs(tool.ExecutablePath) || filepath.Clean(tool.ExecutablePath) != tool.ExecutablePath {
			return fmt.Errorf("toolchain %q executable path must be absolute and clean", tool.Name)
		}
		if !digestPattern.MatchString(tool.BinaryDigest) {
			return fmt.Errorf("toolchain %q binary digest must be sha256:<64 lowercase hex characters>", tool.Name)
		}
	}
	if len(r.SelectedVerifiers) > 256 {
		return errors.New("selected verifiers exceed the 256-item limit")
	}
	seenVerifiers := map[string]bool{}
	for _, verifier := range r.SelectedVerifiers {
		if !stableIDPattern.MatchString(verifier) || seenVerifiers[verifier] {
			return fmt.Errorf("invalid or duplicate selected verifier %q", verifier)
		}
		seenVerifiers[verifier] = true
	}
	if len(r.Receipts) > 1024 {
		return errors.New("receipts exceed the 1024-item limit")
	}
	for _, value := range []*int64{r.InputTokens, r.OutputTokens, r.EstimatedCostMicros} {
		if value != nil && *value < 0 {
			return errors.New("token and cost observations cannot be negative")
		}
	}
	for _, receipt := range r.Receipts {
		if !stableIDPattern.MatchString(receipt.VerifierID) {
			return fmt.Errorf("invalid receipt verifier id %q", receipt.VerifierID)
		}
		if len(receipt.ClaimIDs) == 0 || len(receipt.ClaimIDs) > 256 {
			return errors.New("receipt claim ids must contain between 1 and 256 items")
		}
		seenClaims := map[string]bool{}
		for _, claimID := range receipt.ClaimIDs {
			if !stableIDPattern.MatchString(claimID) || seenClaims[claimID] {
				return fmt.Errorf("invalid or duplicate receipt claim id %q", claimID)
			}
			seenClaims[claimID] = true
		}
		switch receipt.Status {
		case "passed", "failed", "blocked", "not_run", "inconclusive":
		default:
			return fmt.Errorf("invalid receipt status %q", receipt.Status)
		}
	}
	return nil
}

func validateLauncherResultForManifest(result LauncherResult, manifest Manifest, arm Arm, mutableRunRoot string) ([]ToolchainProvenance, error) {
	claims := make(map[string]bool, len(manifest.Acceptance))
	for _, criterion := range manifest.Acceptance {
		claims[criterion.ID] = true
	}
	targets := make(map[string]map[string]bool, len(manifest.Oracle.Commands)+len(manifest.Oracle.GlyphrunSpecs))
	for _, target := range verificationTargets(manifest) {
		targets[target.ID] = make(map[string]bool, len(target.ClaimIDs))
		for _, claimID := range target.ClaimIDs {
			targets[target.ID][claimID] = true
		}
	}
	for _, verifier := range result.SelectedVerifiers {
		if targets[verifier] == nil {
			return nil, fmt.Errorf("selected verifier %q is not a scenario verification target", verifier)
		}
	}
	for _, receipt := range result.Receipts {
		coveredClaims := targets[receipt.VerifierID]
		if coveredClaims == nil {
			return nil, fmt.Errorf("receipt verifier %q is not a scenario verification target", receipt.VerifierID)
		}
		for _, claimID := range receipt.ClaimIDs {
			if !claims[claimID] {
				return nil, fmt.Errorf("receipt references unknown acceptance criterion %q", claimID)
			}
			if !coveredClaims[claimID] {
				return nil, fmt.Errorf("receipt verifier %q does not cover acceptance criterion %q", receipt.VerifierID, claimID)
			}
		}
	}
	if !modelsEqual(result.EffectiveModel, manifest.Model) {
		return nil, errors.New("launcher effective model configuration does not match the manifest")
	}
	if result.ToolCalls > manifest.Budget.MaxToolCalls {
		return nil, fmt.Errorf("launcher reported %d tool calls, exceeding the %d-call budget", result.ToolCalls, manifest.Budget.MaxToolCalls)
	}
	if result.EstimatedCostMicros != nil && manifest.Budget.MaxEstimatedCostMicros > 0 && *result.EstimatedCostMicros > manifest.Budget.MaxEstimatedCostMicros {
		return nil, fmt.Errorf("launcher reported cost %d, exceeding the %d-micro budget", *result.EstimatedCostMicros, manifest.Budget.MaxEstimatedCostMicros)
	}
	if err := validateInstrumentedObservation(result.Observation); err != nil {
		return nil, err
	}
	return validateToolchainProvenance(result.Toolchain, arm, mutableRunRoot)
}

func validateInstrumentedObservation(observation LauncherObservation) error {
	ranges := []struct {
		name         string
		value, total int
	}{
		{"evidence.sourced", observation.Evidence.Sourced, observation.Evidence.Items},
		{"evidence.claimsWithVerifiableSource", observation.Evidence.ClaimsWithVerifiableSource, observation.Evidence.ClaimsRequiringProof},
		{"evidence.candidatesKeptAsCandidates", observation.Evidence.CandidatesKeptAsCandidates, observation.Evidence.Candidates},
		{"disproof.withDisproofPath", observation.Disproof.WithDisproofPath, observation.Disproof.Hypotheses},
		{"disproof.evidenceGroundedResolutions", observation.Disproof.EvidenceGroundedResolutions, observation.Disproof.Resolutions},
		{"recovery.restoredState", observation.Recovery.RestoredState, observation.Recovery.ExpectedState},
	}
	for _, observed := range ranges {
		if observed.value < 0 || observed.total < 0 || observed.value > observed.total {
			return fmt.Errorf("%s must satisfy 0 <= value <= total (got %d/%d)", observed.name, observed.value, observed.total)
		}
	}
	return nil
}

func modelsEqual(got, want Model) bool {
	if got.Identifier != want.Identifier || got.Build != want.Build || got.Temperature != want.Temperature ||
		got.SeedUnsupportedReason != want.SeedUnsupportedReason || got.ContextBudgetTokens != want.ContextBudgetTokens {
		return false
	}
	if got.Seed == nil || want.Seed == nil {
		return got.Seed == nil && want.Seed == nil
	}
	return *got.Seed == *want.Seed
}

func encodeLauncherRequest(request LauncherRequest) ([]byte, string, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(data)
	return append(data, '\n'), "sha256:" + hex.EncodeToString(digest[:]), nil
}

func decodeLauncherResult(data []byte, expectedDigest string) (LauncherResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result LauncherResult
	if err := decoder.Decode(&result); err != nil {
		return LauncherResult{}, fmt.Errorf("decode launcher result: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return LauncherResult{}, errors.New("launcher stdout contains multiple JSON values")
		}
		return LauncherResult{}, err
	}
	if err := result.Validate(expectedDigest); err != nil {
		return LauncherResult{}, err
	}
	return result, nil
}
