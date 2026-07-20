package trajectory

import (
	"time"

	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
)

// This file holds the trajectory report shapes — the durable, owner-only output
// of a run — separate from the execution logic in runner.go.

type OracleStatus string

const (
	OraclePassed       OracleStatus = "passed"
	OracleFailed       OracleStatus = "failed"
	OracleBlocked      OracleStatus = "blocked"
	OracleNotRun       OracleStatus = "not_run"
	OracleInconclusive OracleStatus = "inconclusive"
	OracleTimeout      OracleStatus = "timeout"
)

type OracleResult struct {
	ID         string       `json:"id"`
	ClaimIDs   []string     `json:"claimIds"`
	Status     OracleStatus `json:"status"`
	ExitCode   int          `json:"exitCode"`
	DurationMs int64        `json:"durationMs"`
	Message    string       `json:"message,omitempty"`
}

type TraceRecord struct {
	Path      string `json:"path,omitempty"`
	Omitted   bool   `json:"omitted,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type ArmReport struct {
	Arm                      Arm                       `json:"arm"`
	RequestDigest            string                    `json:"requestDigest,omitempty"`
	Status                   RunStatus                 `json:"status"`
	Error                    string                    `json:"error,omitempty"`
	Oracle                   []OracleResult            `json:"oracle"`
	OracleSuccess            bool                      `json:"oracleSuccess"`
	ReportedCompletion       baseeval.CompletionLabel  `json:"reportedCompletion,omitempty"`
	ExpectedCompletion       baseeval.CompletionLabel  `json:"expectedCompletion"`
	CompletionComparable     bool                      `json:"completionComparable"`
	HonestCompletion         bool                      `json:"honestCompletion"`
	ChangedFiles             []string                  `json:"changedFiles,omitempty"`
	WrongFiles               []string                  `json:"wrongFiles,omitempty"`
	ScopeDrift               bool                      `json:"scopeDrift"`
	ProtectedChanges         []string                  `json:"protectedChanges,omitempty"`
	OracleIntegrity          bool                      `json:"oracleIntegrity"`
	SelectedVerifiers        []string                  `json:"selectedVerifiers,omitempty"`
	CorrectVerifierSelection bool                      `json:"correctVerifierSelection"`
	CorrectReceipts          int                       `json:"correctReceipts"`
	FalsePasses              int                       `json:"falsePasses"`
	StalePasses              int                       `json:"stalePasses"`
	ToolCalls                int                       `json:"toolCalls"`
	InputTokens              *int64                    `json:"inputTokens,omitempty"`
	OutputTokens             *int64                    `json:"outputTokens,omitempty"`
	EstimatedCostMicros      *int64                    `json:"estimatedCostMicros,omitempty"`
	HumanInterventions       int                       `json:"humanInterventions"`
	LatencyMs                int64                     `json:"latencyMs"`
	OracleLatencyMs          int64                     `json:"oracleLatencyMs"`
	TotalLatencyMs           int64                     `json:"totalLatencyMs"`
	RuntimeRoot              string                    `json:"runtimeRoot"`
	LauncherConfigDigest     string                    `json:"launcherConfigDigest,omitempty"`
	OracleConfigDigest       string                    `json:"oracleConfigDigest,omitempty"`
	Toolchain                []ToolchainProvenance     `json:"toolchain,omitempty"`
	ToolchainValidated       bool                      `json:"toolchainValidated"`
	OracleTools              []ExecutableProvenance    `json:"oracleTools"`
	Warnings                 []string                  `json:"warnings,omitempty"`
	Observation              baseeval.TrialObservation `json:"observation"`
	Trace                    TraceRecord               `json:"trace"`
}

type ArmComparison struct {
	BaselineArm  Arm                   `json:"baselineArm"`
	CandidateArm Arm                   `json:"candidateArm"`
	Score        baseeval.PairedResult `json:"score"`
}

type Report struct {
	SchemaVersion      int                `json:"schemaVersion"`
	RunID              string             `json:"runId"`
	ScenarioID         string             `json:"scenarioId"`
	GeneratedAt        time.Time          `json:"generatedAt"`
	ManifestDigest     string             `json:"manifestDigest"`
	RepositoryDigest   string             `json:"repositoryDigest"`
	OracleDigest       string             `json:"oracleDigest"`
	CortexConfigDigest string             `json:"cortexConfigDigest"`
	Launcher           LauncherProvenance `json:"launcher"`
	Harness            HarnessProvenance  `json:"harness"`
	Model              Model              `json:"model"`
	Arms               []ArmReport        `json:"arms"`
	Comparisons        []ArmComparison    `json:"comparisons"`
	ReportPath         string             `json:"reportPath,omitempty"`
	Warnings           []string           `json:"warnings,omitempty"`
}

type LauncherProvenance struct {
	ConfigDigest string   `json:"configDigest"`
	Argv         []string `json:"argv"`
	ResolvedPath string   `json:"resolvedPath,omitempty"`
	BinaryDigest string   `json:"binaryDigest,omitempty"`
}

type HarnessProvenance struct {
	CortexVersion string `json:"cortexVersion"`
	CortexCommit  string `json:"cortexCommit"`
	CortexDate    string `json:"cortexDate"`
	GoVersion     string `json:"goVersion"`
	GOOS          string `json:"goos"`
	GOARCH        string `json:"goarch"`
	VCSRevision   string `json:"vcsRevision,omitempty"`
	VCSModified   bool   `json:"vcsModified"`
}

type ExecutableProvenance struct {
	ID           string `json:"id"`
	Argv0        string `json:"argv0"`
	ResolvedPath string `json:"resolvedPath,omitempty"`
	BinaryDigest string `json:"binaryDigest,omitempty"`
}

type RunInput struct {
	Manifest  Manifest
	Launcher  LauncherConfig
	StateRoot string
	RunID     string
	Now       func() time.Time
	Processes ProcessRunner
}
