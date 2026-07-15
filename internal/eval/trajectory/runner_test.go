package trajectory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
)

type fakeProcessRunner struct {
	calls []ProcessRequest
}

type deadlineProcessRunner struct{}

func (deadlineProcessRunner) Run(ctx context.Context, _ ProcessRequest) ProcessResult {
	<-ctx.Done()
	return ProcessResult{ExitCode: -1, TimedOut: true, Err: ctx.Err()}
}

type canceledProcessRunner struct{}

type protectedMutationRunner struct {
	fakeProcessRunner
}

type launcherMutationRunner struct {
	fakeProcessRunner
	path    string
	mutated bool
}

type frozenBaselineMutationRunner struct {
	fakeProcessRunner
}

type recordingProcessRunner struct {
	calls []ProcessRequest
}

type configMutationRunner struct {
	fakeProcessRunner
	launcherConfigs map[Arm]string
	launcherDirs    map[Arm]string
	oracleDirs      map[Arm]string
}

func (canceledProcessRunner) Run(context.Context, ProcessRequest) ProcessResult {
	return ProcessResult{ExitCode: -1, Canceled: true, Err: context.Canceled}
}

func (p *protectedMutationRunner) Run(ctx context.Context, request ProcessRequest) ProcessResult {
	result := p.fakeProcessRunner.Run(ctx, request)
	if request.Kind == ProcessLauncher {
		_ = os.WriteFile(filepath.Join(request.Dir, "cmd", "hello", "main_test.go"), []byte("package main\n// launcher attempted to replace the oracle\n"), 0o600)
	}
	return result
}

func (m *launcherMutationRunner) Run(ctx context.Context, request ProcessRequest) ProcessResult {
	result := m.fakeProcessRunner.Run(ctx, request)
	if request.Kind == ProcessLauncher && !m.mutated {
		m.mutated = true
		_ = os.WriteFile(m.path, []byte("#!/bin/sh\nexit 1\n"), 0o700)
	}
	return result
}

func (m *frozenBaselineMutationRunner) Run(ctx context.Context, request ProcessRequest) ProcessResult {
	result := m.fakeProcessRunner.Run(ctx, request)
	if request.Kind == ProcessLauncher {
		baselineFile := filepath.Join(request.Dir, "..", "..", "fixture-baseline", "cmd", "hello", "main_test.go")
		_ = os.WriteFile(baselineFile, []byte("package main\n// tampered frozen baseline\n"), 0o600)
	}
	return result
}

func (r *recordingProcessRunner) Run(_ context.Context, request ProcessRequest) ProcessResult {
	r.calls = append(r.calls, request)
	return ProcessResult{ExitCode: 0}
}

func (r *configMutationRunner) Run(ctx context.Context, request ProcessRequest) ProcessResult {
	switch request.Kind {
	case ProcessLauncher:
		if r.launcherConfigs == nil {
			r.launcherConfigs = map[Arm]string{}
			r.launcherDirs = map[Arm]string{}
			r.oracleDirs = map[Arm]string{}
		}
		configPath := filepath.Join(request.Environment["CORTEX_CONFIG_DIR"], "config.yaml")
		data, _ := os.ReadFile(configPath)
		r.launcherConfigs[request.Arm] = string(data)
		r.launcherDirs[request.Arm] = request.Environment["CORTEX_CONFIG_DIR"]
	case ProcessOracle:
		r.oracleDirs[request.Arm] = request.Environment["CORTEX_CONFIG_DIR"]
	}
	result := r.fakeProcessRunner.Run(ctx, request)
	if request.Kind == ProcessLauncher && request.Arm == ArmRawTools {
		configPath := filepath.Join(request.Environment["CORTEX_CONFIG_DIR"], "config.yaml")
		_ = os.Chmod(configPath, 0o600)
		_ = os.WriteFile(configPath, []byte("recall:\n  enabled: true\n"), 0o600)
	}
	return result
}

func (f *fakeProcessRunner) Run(ctx context.Context, request ProcessRequest) ProcessResult {
	f.calls = append(f.calls, request)
	if request.Kind == ProcessOracle {
		_ = os.WriteFile(filepath.Join(request.Dir, "oracle-output.txt"), []byte("must not affect model diff"), 0o600)
		data, err := os.ReadFile(filepath.Join(request.Dir, "cmd", "hello", "main.go"))
		if err != nil || !bytes.Contains(data, []byte("// repaired by ")) {
			return ProcessResult{ExitCode: 1, Err: errors.New("oracle failed"), Stderr: []byte("expected regression")}
		}
		return ProcessResult{ExitCode: 0}
	}
	var launcherRequest LauncherRequest
	if err := json.Unmarshal(request.Stdin, &launcherRequest); err != nil {
		return ProcessResult{ExitCode: 1, Err: err}
	}
	if request.Arm != ArmRawTools {
		path := filepath.Join(request.Dir, "cmd", "hello", "main.go")
		data, _ := os.ReadFile(path)
		_ = os.WriteFile(path, append(data, []byte("\n// repaired by "+string(request.Arm)+"\n")...), 0o644)
	}
	if request.Arm == ArmCortexBob {
		_ = os.WriteFile(filepath.Join(request.Dir, "unexpected.txt"), []byte("scope drift"), 0o644)
	}
	revision, err := adapters.NewGit().CurrentRevision(ctx, request.Dir)
	if err != nil {
		return ProcessResult{ExitCode: 1, Err: err}
	}
	requestJSON := bytes.TrimSuffix(request.Stdin, []byte{'\n'})
	digest := sha256.Sum256(requestJSON)
	completion := baseeval.CompletionVerified
	status := RunCompleted
	receiptStatus := "passed"
	if request.Arm == ArmRawTools {
		completion = baseeval.CompletionFailed
		receiptStatus = "failed"
	}
	if request.Arm == ArmCortexBobLocalAgent {
		status = RunIncomplete
	}
	value := int64(100)
	toolchain, err := fakeToolchain(request.Arm)
	if err != nil {
		return ProcessResult{ExitCode: 1, Err: err}
	}
	result := LauncherResult{
		SchemaVersion: ProtocolSchemaVersion,
		RequestDigest: "sha256:" + hex.EncodeToString(digest[:]),
		Status:        status, ReportedCompletion: completion,
		EffectiveModel:    launcherRequest.Model,
		Observation:       instrumentedObservation(),
		Toolchain:         toolchain,
		SelectedVerifiers: []string{"go_tests", "terminal_help_flow"},
		Receipts: []ReceiptObservation{{
			ClaimIDs: []string{"terminal_help"}, VerifierID: "go_tests", Status: receiptStatus,
			Revision: revision.Commit, DirtyDigest: revision.DirtyDigest,
		}},
		InputTokens: &value, OutputTokens: &value, EstimatedCostMicros: &value,
		ToolCalls: 3, HumanInterventions: 0,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return ProcessResult{ExitCode: 1, Err: err}
	}
	if request.Arm == ArmCortexBobLocalAgent {
		return ProcessResult{Stdout: encoded, Stderr: []byte("oversize trace"), StderrTruncated: true}
	}
	return ProcessResult{Stdout: encoded, Stderr: []byte("API_TOKEN=abcdefghijklmnop")}
}

func instrumentedObservation() LauncherObservation {
	return LauncherObservation{
		Evidence:         baseeval.EvidenceObservation{Required: true, Items: 1, Sourced: 1, ClaimsRequiringProof: 1, ClaimsWithVerifiableSource: 1},
		Disproof:         baseeval.DisproofObservation{Required: true, Hypotheses: 1, WithDisproofPath: 1},
		Recovery:         baseeval.RecoveryObservation{Required: true, Resumed: true, ExpectedState: 1, RestoredState: 1},
		BoundaryDeclared: true,
	}
}

func fakeToolchain(arm Arm) ([]ToolchainProvenance, error) {
	names := []string{"go"}
	switch arm {
	case ArmCortex:
		names = []string{"cortex"}
	case ArmCortexBob:
		names = []string{"cortex", "bob"}
	case ArmCortexBobLocalAgent:
		names = []string{"cortex", "bob", "local-agent"}
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	digest, err := regularFileDigest(executable)
	if err != nil {
		return nil, err
	}
	result := make([]ToolchainProvenance, 0, len(names))
	for _, name := range names {
		result = append(result, ToolchainProvenance{Name: name, Version: "test-build", ExecutablePath: executable, BinaryDigest: digest})
	}
	return result, nil
}

func testLauncherConfig(t *testing.T) LauncherConfig {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	return LauncherConfig{SchemaVersion: 1, Argv: []string{executable}}
}

func installFakeOraclePath(t *testing.T) {
	t.Helper()
	directory := t.TempDir()
	for _, name := range []string{"go", "glyph"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("test oracle executable: "+name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func preparedTestOracle(t *testing.T, id string) preparedOracle {
	t.Helper()
	executable := mustExecutable(t)
	digest, err := regularFileDigest(executable)
	if err != nil {
		t.Fatal(err)
	}
	return preparedOracle{
		id: id, argv: []string{executable}, claimIDs: []string{"claim"},
		timeout: time.Second, expectedBinaryDigest: digest,
	}
}

func mustExecutable(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	return executable
}

func TestRunRequiresApprovalBeforeAnyProcess(t *testing.T) {
	t.Setenv(approvalEnv, "")
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeProcessRunner{}
	_, err = Run(context.Background(), RunInput{
		Manifest: manifest, Launcher: testLauncherConfig(t),
		StateRoot: t.TempDir(), RunID: "run-test", Processes: fake,
	})
	if err == nil || !strings.Contains(err.Error(), approvalEnv) || len(fake.calls) != 0 {
		t.Fatalf("approval gate error=%v calls=%d", err, len(fake.calls))
	}
}

func TestRunRetainsAllArmsAndDerivesIndependentMetrics(t *testing.T) {
	t.Setenv(approvalEnv, "1")
	installFakeOraclePath(t)
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("recall:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORTEX_CONFIG_DIR", configDir)
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	fake := &fakeProcessRunner{}
	launcherBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcherBinary, err = filepath.EvalSymlinks(launcherBinary)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), RunInput{
		Manifest: manifest, Launcher: LauncherConfig{SchemaVersion: 1, Argv: []string{launcherBinary, "--trusted"}},
		StateRoot: stateRoot, Processes: fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Arms) != 4 || len(report.Comparisons) != 3 {
		t.Fatalf("report arms=%d comparisons=%d", len(report.Arms), len(report.Comparisons))
	}
	if !runIDPattern.MatchString(report.RunID) || !strings.HasPrefix(report.RunID, "run_") {
		t.Fatalf("generated run id %q is invalid", report.RunID)
	}
	if len(fake.calls) != 12 || fake.calls[0].Kind != ProcessLauncher || len(fake.calls[0].Argv) != 2 || fake.calls[0].Argv[0] != launcherBinary || fake.calls[0].Argv[1] != "--trusted" {
		t.Fatalf("process calls did not preserve exact launcher/oracle contract: %+v", fake.calls)
	}
	stateRoots := map[string]bool{}
	for index := 0; index < len(fake.calls); index += 3 {
		call := fake.calls[index]
		stateRoot := call.Environment["CORTEX_STATE_DIR"]
		if stateRoot == "" || call.Environment["CORTEX_CASES_DIR"] == "" || call.Environment["XDG_DATA_HOME"] == "" {
			t.Fatalf("arm environment was not isolated: %+v", call.Environment)
		}
		stateRoots[stateRoot] = true
		if fake.calls[index+1].Environment["CORTEX_STATE_DIR"] == stateRoot {
			t.Fatalf("oracle and launcher share mutable state for %s", call.Arm)
		}
	}
	if len(stateRoots) != 4 {
		t.Fatalf("launcher state roots were shared: %v", stateRoots)
	}
	if !digestPattern.MatchString(report.ManifestDigest) || !digestPattern.MatchString(report.OracleDigest) || !digestPattern.MatchString(report.CortexConfigDigest) || !digestPattern.MatchString(report.Launcher.ConfigDigest) || !digestPattern.MatchString(report.Launcher.BinaryDigest) {
		t.Fatalf("report provenance is incomplete: %+v", report)
	}
	if report.RepositoryDigest != manifest.Repository.Digest || report.Arms[0].RequestDigest == "" {
		t.Fatalf("repository/request provenance is incomplete: %+v", report)
	}
	if report.Harness.GoVersion == "" || report.Harness.GOOS == "" || len(report.Arms[0].OracleTools) != 2 || report.Arms[0].TotalLatencyMs < report.Arms[0].LatencyMs {
		t.Fatalf("harness/tool/latency provenance is incomplete: %+v", report)
	}
	var request LauncherRequest
	if err := json.Unmarshal(fake.calls[0].Stdin, &request); err != nil {
		t.Fatal(err)
	}
	if request.Arm != ArmRawTools || len(request.VerificationTargets) != 2 || request.VerificationTargets[0].ID != "go_tests" {
		t.Fatalf("launcher request targets = %+v", request.VerificationTargets)
	}
	if report.Arms[0].OracleSuccess || report.Arms[0].ExpectedCompletion != baseeval.CompletionFailed || !report.Arms[0].HonestCompletion {
		t.Fatalf("raw-tools oracle/completion = %+v", report.Arms[0])
	}
	if report.Arms[2].Arm != ArmCortexBob || !report.Arms[2].ScopeDrift || report.Arms[2].OracleIntegrity || report.Arms[2].OracleSuccess || report.Arms[2].ExpectedCompletion == baseeval.CompletionVerified || len(report.Arms[2].WrongFiles) != 1 || report.Arms[2].WrongFiles[0] != "unexpected.txt" {
		t.Fatalf("wrong-file derivation = %+v", report.Arms[2])
	}
	for _, arm := range report.Arms {
		if slices.Contains(arm.ChangedFiles, "oracle-output.txt") {
			t.Fatalf("oracle mutation contaminated %s diff: %v", arm.Arm, arm.ChangedFiles)
		}
		modelWorkspace := filepath.Join(stateRoot, manifest.ID, report.RunID, "workspaces", string(arm.Arm))
		if _, err := os.Stat(filepath.Join(modelWorkspace, "oracle-output.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("oracle mutation reached model workspace for %s: %v", arm.Arm, err)
		}
	}
	if report.Arms[3].Status != RunIncomplete || !report.Arms[3].Trace.Omitted || !report.Arms[3].Trace.Truncated {
		t.Fatalf("incomplete/truncated arm was not retained: %+v", report.Arms[3])
	}
	if report.Arms[1].InputTokens == nil || report.Arms[1].CorrectVerifierSelection == false {
		t.Fatalf("instrumentation/verifier selection missing: %+v", report.Arms[1])
	}
	if !report.Arms[1].ToolchainValidated || len(report.Arms[1].Toolchain) != 1 || report.Arms[1].Toolchain[0].Name != "cortex" || !report.Arms[1].Observation.Scope.BoundaryDeclared {
		t.Fatalf("toolchain/boundary instrumentation missing: %+v", report.Arms[1])
	}
	tracePath := filepath.Join(stateRoot, manifest.ID, report.RunID, filepath.FromSlash(report.Arms[1].Trace.Path))
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(trace), "abcdefghijklmnop") || !strings.Contains(string(trace), "«redacted»") {
		t.Fatalf("trace was not redacted: %s", trace)
	}
	info, err := os.Stat(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("trace permissions=%v", info.Mode().Perm())
	}
	reportPath := filepath.Join(stateRoot, manifest.ID, report.RunID, report.ReportPath)
	info, err = os.Stat(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("report permissions=%v", info.Mode().Perm())
	}
	for _, relative := range []string{
		"manifest.json", "launcher.json", filepath.Join("oracle-specs", "oracles", "terminal-command-regression", "hello.yml"),
		filepath.Join("runtime", "cortex-config-baseline", "config.yaml"),
	} {
		info, err := os.Stat(filepath.Join(stateRoot, manifest.ID, report.RunID, relative))
		if err != nil {
			t.Fatal(err)
		}
		wantMode := os.FileMode(0o600)
		if strings.Contains(relative, "cortex-config-baseline") {
			wantMode = 0o400
		}
		if info.Mode().Perm() != wantMode {
			t.Fatalf("%s permissions=%v want=%v", relative, info.Mode().Perm(), wantMode)
		}
	}
}

func TestProtectedOracleMutationIsExcludedAndCannotTurnCompletionGreen(t *testing.T) {
	t.Setenv(approvalEnv, "1")
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Arms = []Arm{ArmRawTools, ArmCortex}
	stateRoot := t.TempDir()
	runner := &protectedMutationRunner{}
	report, err := Run(context.Background(), RunInput{
		Manifest: manifest, Launcher: testLauncherConfig(t), StateRoot: stateRoot, RunID: "protected-mutation", Processes: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := report.Arms[1]
	if candidate.OracleIntegrity || candidate.OracleSuccess || candidate.ExpectedCompletion == baseeval.CompletionVerified || !slices.Contains(candidate.ProtectedChanges, "cmd/hello/main_test.go") {
		t.Fatalf("protected mutation was allowed to produce a green result: %+v", candidate)
	}
	oracleTest := filepath.Join(stateRoot, manifest.ID, report.RunID, "oracle-workspaces", string(ArmCortex), "cmd", "hello", "main_test.go")
	data, err := os.ReadFile(oracleTest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("attempted to replace")) || !bytes.Contains(data, []byte("TestHelpContract")) {
		t.Fatalf("oracle workspace consumed the mutable protected test: %s", data)
	}
}

func TestCortexConfigIsPrivatePerPhaseAndArm(t *testing.T) {
	t.Setenv(approvalEnv, "1")
	configDir := t.TempDir()
	original := "recall:\n  enabled: false\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORTEX_CONFIG_DIR", configDir)
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Arms = []Arm{ArmRawTools, ArmCortex}
	runner := &configMutationRunner{}
	report, err := Run(context.Background(), RunInput{
		Manifest: manifest, Launcher: testLauncherConfig(t), StateRoot: t.TempDir(),
		RunID: "config-isolation", Processes: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.launcherConfigs[ArmRawTools] != original || runner.launcherConfigs[ArmCortex] != original {
		t.Fatalf("launcher configs were contaminated across arms: %q / %q", runner.launcherConfigs[ArmRawTools], runner.launcherConfigs[ArmCortex])
	}
	if runner.launcherDirs[ArmRawTools] == runner.launcherDirs[ArmCortex] {
		t.Fatalf("launcher arms shared config dir %q", runner.launcherDirs[ArmRawTools])
	}
	if len(report.Arms) != 2 || report.Arms[0].OracleIntegrity || report.Arms[0].Status != RunFailed || !strings.Contains(report.Arms[0].Error, "config changed during execution") {
		t.Fatalf("mutated private config did not invalidate only its arm: %+v", report.Arms)
	}
	if !report.Arms[1].OracleIntegrity {
		t.Fatalf("later arm inherited config contamination: %+v", report.Arms[1])
	}
	for _, arm := range report.Arms {
		if arm.LauncherConfigDigest != report.CortexConfigDigest || arm.OracleConfigDigest != report.CortexConfigDigest {
			t.Fatalf("per-use config provenance does not match baseline: arm=%+v baseline=%s", arm, report.CortexConfigDigest)
		}
	}
	for arm, launcherDir := range runner.launcherDirs {
		if oracleDir := runner.oracleDirs[arm]; oracleDir != "" && oracleDir == launcherDir {
			t.Fatalf("launcher and oracle shared config dir for %s: %q", arm, launcherDir)
		}
	}
}

func TestLauncherDigestIsRevalidatedBeforeEveryArm(t *testing.T) {
	t.Setenv(approvalEnv, "1")
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Arms = []Arm{ArmRawTools, ArmCortex}
	launcherPath := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &launcherMutationRunner{path: launcherPath}
	report, err := Run(context.Background(), RunInput{
		Manifest: manifest, Launcher: LauncherConfig{SchemaVersion: 1, Argv: []string{launcherPath}},
		StateRoot: t.TempDir(), RunID: "launcher-mutation", Processes: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Arms) != 2 || !strings.Contains(report.Arms[1].Error, "changed after its provenance") {
		t.Fatalf("launcher mutation did not fail closed: %+v", report.Arms)
	}
}

func TestFrozenBaselineTamperingFailsBeforeOracleExecution(t *testing.T) {
	t.Setenv(approvalEnv, "1")
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Arms = []Arm{ArmRawTools, ArmCortex}
	runner := &frozenBaselineMutationRunner{}
	report, err := Run(context.Background(), RunInput{
		Manifest: manifest, Launcher: testLauncherConfig(t), StateRoot: t.TempDir(), RunID: "baseline-mutation", Processes: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Arms) != 2 || report.Arms[0].OracleSuccess || report.Arms[0].OracleIntegrity || !strings.Contains(report.Arms[0].Error, "frozen repository fixture digest") {
		t.Fatalf("frozen baseline tampering did not fail closed: %+v", report.Arms)
	}
	for _, call := range runner.calls {
		if call.Kind == ProcessOracle {
			t.Fatalf("oracle executed after frozen baseline tampering: %+v", call)
		}
	}
}

func TestRunRejectsExistingAndInvalidRunIDsBeforeProcesses(t *testing.T) {
	t.Setenv(approvalEnv, "1")
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	existing := filepath.Join(stateRoot, manifest.ID, "existing-run")
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{"existing-run", "UPPERCASE"} {
		fake := &fakeProcessRunner{}
		_, err := Run(context.Background(), RunInput{
			Manifest: manifest, Launcher: testLauncherConfig(t),
			StateRoot: stateRoot, RunID: runID, Processes: fake,
		})
		if err == nil || len(fake.calls) != 0 {
			t.Fatalf("run id %q error=%v calls=%d", runID, err, len(fake.calls))
		}
	}
}

func TestRunRejectsStateInsideScenarioRepository(t *testing.T) {
	t.Setenv(approvalEnv, "1")
	manifest, err := LoadManifest(sampleManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := containingRepository(manifest.baseDir)
	if repositoryRoot == "" {
		t.Fatal("sample manifest must live in the repository")
	}
	fake := &fakeProcessRunner{}
	_, err = Run(context.Background(), RunInput{
		Manifest: manifest, Launcher: testLauncherConfig(t),
		StateRoot: filepath.Join(repositoryRoot, ".trajectory-output"), RunID: "contained-run", Processes: fake,
	})
	if err == nil || !strings.Contains(err.Error(), "outside the scenario repository") || len(fake.calls) != 0 {
		t.Fatalf("contained state error=%v calls=%d", err, len(fake.calls))
	}
}

func TestOracleAndReceiptNonPassingStatusesNeverPass(t *testing.T) {
	for _, status := range []OracleStatus{OracleFailed, OracleBlocked, OracleNotRun, OracleInconclusive, OracleTimeout} {
		if oracleSucceeded([]OracleResult{{ID: "check", Status: status}}) {
			t.Fatalf("oracle status %s became passed", status)
		}
	}
	current := adapters.Revision{Commit: "commit", DirtyDigest: "dirty"}
	for _, status := range []string{"failed", "blocked", "not_run", "inconclusive"} {
		correctReceipts, falsePasses, stalePasses := receiptIntegrity([]ReceiptObservation{{
			ClaimIDs: []string{"claim"}, VerifierID: "check", Status: status,
		}}, []OracleResult{{ID: "check", ClaimIDs: []string{"claim"}, Status: OracleFailed}}, current, nil)
		if correctReceipts != 1 || falsePasses != 0 || stalePasses != 0 {
			t.Fatalf("non-passing receipt %s metrics = %d/%d/%d", status, correctReceipts, falsePasses, stalePasses)
		}
	}
	correct, falsePasses, stalePasses := receiptIntegrity([]ReceiptObservation{
		{ClaimIDs: []string{"claim"}, VerifierID: "check", Status: "passed", Revision: "old", DirtyDigest: "old"},
		{ClaimIDs: []string{"claim"}, VerifierID: "check", Status: "passed", Revision: "old", DirtyDigest: "old"},
	}, []OracleResult{{ID: "check", ClaimIDs: []string{"claim"}, Status: OracleFailed}}, current, nil)
	if correct != 0 || falsePasses != 1 || stalePasses != 1 {
		t.Fatalf("duplicate invalid receipts must count per claim, got %d/%d/%d", correct, falsePasses, stalePasses)
	}
}

func TestReceiptIntegrityUsesVerifierClaimPairs(t *testing.T) {
	current := adapters.Revision{Commit: "commit", DirtyDigest: "dirty"}
	oracles := []OracleResult{
		{ID: "go_tests", ClaimIDs: []string{"terminal_help"}, Status: OraclePassed},
		{ID: "terminal_help_flow", ClaimIDs: []string{"terminal_help"}, Status: OracleFailed},
	}
	receipts := []ReceiptObservation{
		{ClaimIDs: []string{"terminal_help"}, VerifierID: "go_tests", Status: "passed", Revision: "commit", DirtyDigest: "dirty"},
		{ClaimIDs: []string{"terminal_help"}, VerifierID: "terminal_help_flow", Status: "failed"},
	}
	correct, falsePasses, stalePasses := receiptIntegrity(receipts, oracles, current, nil)
	if correct != 1 || falsePasses != 0 || stalePasses != 0 {
		t.Fatalf("truthful mixed verifier results = %d/%d/%d", correct, falsePasses, stalePasses)
	}

	receipts[1] = ReceiptObservation{
		ClaimIDs: []string{"terminal_help"}, VerifierID: "terminal_help_flow", Status: "passed",
		Revision: "commit", DirtyDigest: "dirty",
	}
	correct, falsePasses, stalePasses = receiptIntegrity(receipts, oracles, current, nil)
	if correct != 0 || falsePasses != 1 || stalePasses != 0 {
		t.Fatalf("false pass from the failing verifier = %d/%d/%d", correct, falsePasses, stalePasses)
	}

	receipts = []ReceiptObservation{
		{ClaimIDs: []string{"terminal_help"}, VerifierID: "go_tests", Status: "failed"},
		{ClaimIDs: []string{"terminal_help"}, VerifierID: "terminal_help_flow", Status: "failed"},
	}
	correct, falsePasses, stalePasses = receiptIntegrity(receipts, oracles, current, nil)
	if correct != 0 || falsePasses != 0 || stalePasses != 0 {
		t.Fatalf("non-passing receipt cannot satisfy a passing verifier, got %d/%d/%d", correct, falsePasses, stalePasses)
	}
}

func TestSnapshotFilesIsContextAndSizeBounded(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxSnapshotFile + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotFiles(context.Background(), root); err == nil || !strings.Contains(err.Error(), "snapshot limit") {
		t.Fatalf("oversized workspace file error = %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("bounded"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := snapshotFiles(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled snapshot error = %v", err)
	}
}

func TestExpectedCompletionNeverTurnsNonPassingOracleGreen(t *testing.T) {
	tests := []struct {
		status OracleStatus
		want   baseeval.CompletionLabel
	}{
		{OraclePassed, baseeval.CompletionVerified},
		{OracleFailed, baseeval.CompletionFailed},
		{OracleBlocked, baseeval.CompletionUnverified},
		{OracleNotRun, baseeval.CompletionUnverified},
		{OracleInconclusive, baseeval.CompletionUnverified},
		{OracleTimeout, baseeval.CompletionIncomplete},
	}
	for _, test := range tests {
		if got := expectedCompletion([]OracleResult{{Status: test.status}}, true); got != test.want {
			t.Fatalf("status %s completion=%s want=%s", test.status, got, test.want)
		}
	}
	if got := expectedCompletion([]OracleResult{{Status: OraclePassed}}, false); got != baseeval.CompletionUnverified {
		t.Fatalf("integrity-invalid passing oracle completion=%s", got)
	}
}

func TestAggregateOracleDeadlineBoundsTheWholeSet(t *testing.T) {
	oracles := []preparedOracle{preparedTestOracle(t, "first"), preparedTestOracle(t, "second")}
	started := time.Now()
	results := runOracles(context.Background(), deadlineProcessRunner{}, oracles, 20*time.Millisecond, t.TempDir(), ArmRawTools, nil)
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("aggregate oracle deadline took %s", elapsed)
	}
	if len(results) != 2 || results[0].Status != OracleTimeout || results[1].Status != OracleNotRun {
		t.Fatalf("oracle deadline results=%+v", results)
	}
}

func TestCanceledOracleIsNotClassifiedAsBlockedOrPassed(t *testing.T) {
	result := runOracle(
		context.Background(), canceledProcessRunner{}, ArmRawTools, t.TempDir(),
		preparedTestOracle(t, "check"), nil,
	)
	if result.Status != OracleNotRun || result.Message != "oracle canceled" {
		t.Fatalf("canceled oracle=%+v", result)
	}
}

func TestOracleUsesResolvedExecutableAfterPATHReplacement(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	name := "trajectory-oracle-tool"
	firstPath := filepath.Join(first, name)
	secondPath := filepath.Join(second, name)
	if err := os.WriteFile(firstPath, []byte("first executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("replacement executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", first+string(os.PathListSeparator)+os.Getenv("PATH"))
	manifest := Manifest{Oracle: Oracle{Commands: []OracleCommand{{
		ID: "path_check", Argv: []string{name, "verify"}, ClaimIDs: []string{"claim"}, Timeout: Duration(time.Second),
	}}}}
	oracles, provenance, warnings := prepareOracleInvocations(manifest, t.TempDir())
	if len(oracles) != 1 || len(provenance) != 1 || len(warnings) != 0 {
		t.Fatalf("prepared oracle=%+v provenance=%+v warnings=%v", oracles, provenance, warnings)
	}
	expected, err := filepath.EvalSymlinks(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", second+string(os.PathListSeparator)+os.Getenv("PATH"))
	runner := &recordingProcessRunner{}
	results := runOracles(context.Background(), runner, oracles, time.Second, t.TempDir(), ArmRawTools, nil)
	if len(results) != 1 || results[0].Status != OraclePassed || len(runner.calls) != 1 {
		t.Fatalf("oracle results=%+v calls=%+v", results, runner.calls)
	}
	call := runner.calls[0]
	if call.Argv[0] != expected || call.Argv[0] == secondPath || call.ExpectedBinaryDigest != provenance[0].BinaryDigest || provenance[0].ResolvedPath != expected {
		t.Fatalf("oracle execution was not pinned to provenance: call=%+v provenance=%+v", call, provenance[0])
	}
}

func TestOracleDigestMutationBlocksOnlyThatInvocation(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "oracle")
	if err := os.WriteFile(executable, []byte("original executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Oracle: Oracle{Commands: []OracleCommand{
		{ID: "mutated", Argv: []string{executable}, ClaimIDs: []string{"claim"}, Timeout: Duration(time.Second)},
		{ID: "available", Argv: []string{mustExecutable(t)}, ClaimIDs: []string{"claim"}, Timeout: Duration(time.Second)},
	}}}
	oracles, _, warnings := prepareOracleInvocations(manifest, t.TempDir())
	if len(warnings) != 0 {
		t.Fatalf("unexpected preparation warnings: %v", warnings)
	}
	if err := os.WriteFile(executable, []byte("mutated executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &recordingProcessRunner{}
	results := runOracles(context.Background(), runner, oracles, time.Second, t.TempDir(), ArmRawTools, nil)
	if len(results) != 2 || results[0].Status != OracleBlocked || !strings.Contains(results[0].Message, "digest changed") || results[1].Status != OraclePassed || len(runner.calls) != 1 || runner.calls[0].ID != "available" {
		t.Fatalf("digest mutation affected the wrong invocations: results=%+v calls=%+v", results, runner.calls)
	}
}

func TestMissingOracleExecutableIsBlockedWithoutStoppingAvailableOracle(t *testing.T) {
	manifest := Manifest{Oracle: Oracle{Commands: []OracleCommand{
		{ID: "missing", Argv: []string{"definitely-missing-cortex-oracle"}, ClaimIDs: []string{"claim"}, Timeout: Duration(time.Second)},
		{ID: "available", Argv: []string{mustExecutable(t)}, ClaimIDs: []string{"claim"}, Timeout: Duration(time.Second)},
	}}}
	oracles, provenance, warnings := prepareOracleInvocations(manifest, t.TempDir())
	if len(provenance) != 2 || len(warnings) != 1 || provenance[0].BinaryDigest != "" {
		t.Fatalf("missing provenance=%+v warnings=%v", provenance, warnings)
	}
	runner := &recordingProcessRunner{}
	results := runOracles(context.Background(), runner, oracles, time.Second, t.TempDir(), ArmRawTools, nil)
	if len(results) != 2 || results[0].Status != OracleBlocked || results[1].Status != OraclePassed || len(runner.calls) != 1 || runner.calls[0].ID != "available" {
		t.Fatalf("missing oracle handling results=%+v calls=%+v", results, runner.calls)
	}
}

func TestMissingLauncherCompletionIsNotImputed(t *testing.T) {
	report := ArmReport{ExpectedCompletion: baseeval.CompletionFailed}
	observation := deriveObservation(report, LauncherObservation{}, 1)
	if report.CompletionComparable || report.HonestCompletion || observation.Completion.Expected != "" || observation.Completion.Reported != "" {
		t.Fatalf("missing completion was imputed: report=%+v observation=%+v", report, observation.Completion)
	}
}

func TestBoundaryDeclarationComesFromLauncherInstrumentation(t *testing.T) {
	report := ArmReport{}
	without := deriveObservation(report, LauncherObservation{}, 1)
	with := deriveObservation(report, LauncherObservation{BoundaryDeclared: true}, 1)
	if without.Scope.BoundaryDeclared || !with.Scope.BoundaryDeclared {
		t.Fatalf("boundary declaration was not sourced from launcher instrumentation: without=%+v with=%+v", without.Scope, with.Scope)
	}
}

func TestBoundedBufferDrainsAndMarksTruncation(t *testing.T) {
	buffer := &boundedBuffer{limit: 3}
	if n, err := buffer.Write([]byte("abcdef")); err != nil || n != 6 || string(buffer.Bytes()) != "abc" || !buffer.truncated {
		t.Fatalf("bounded buffer n=%d err=%v bytes=%q truncated=%t", n, err, buffer.Bytes(), buffer.truncated)
	}
}

func TestChildEnvironmentStripsAuthorityAndOracleSecrets(t *testing.T) {
	inherited := []string{
		"PATH=/bin", "CORTEX_APPROVE_TRAJECTORY=1", "CORTEX_APPROVE_COMMANDS=1",
		"OPENAI_API_KEY=secret", "NORMAL=value", "FOO=ghp_16C7e42F292c6912E7710c838347Ae178B4a",
	}
	launcher := childEnvironment(ProcessLauncher, inherited, map[string]string{"CORTEX_STATE_DIR": "/isolated/state"})
	if slices.Contains(launcher, "CORTEX_APPROVE_TRAJECTORY=1") || slices.Contains(launcher, "CORTEX_APPROVE_COMMANDS=1") || !slices.Contains(launcher, "OPENAI_API_KEY=secret") {
		t.Fatalf("launcher environment policy = %v", launcher)
	}
	if !slices.Contains(launcher, "CORTEX_STATE_DIR=/isolated/state") {
		t.Fatalf("launcher overrides missing: %v", launcher)
	}
	oracle := childEnvironment(ProcessOracle, inherited, nil)
	if slices.Contains(oracle, "OPENAI_API_KEY=secret") || slices.Contains(oracle, "FOO=ghp_16C7e42F292c6912E7710c838347Ae178B4a") || !slices.Contains(oracle, "NORMAL=value") || !slices.Contains(oracle, "PATH=/bin") {
		t.Fatalf("oracle environment policy = %v", oracle)
	}
}
