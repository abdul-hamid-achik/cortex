package trajectory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
	"github.com/abdul-hamid-achik/cortex/internal/version"
)

const (
	approvalEnv       = "CORTEX_APPROVE_TRAJECTORY"
	maxLauncherStdout = 1 << 20
	maxOracleOutput   = 64 << 10
	maxSnapshotFile   = 256 << 20
	maxSnapshotTotal  = 1 << 30
)

// Run executes every arm only after explicit trusted-process approval. It
// always runs independent oracles and retains failed/incomplete arms.
func Run(ctx context.Context, input RunInput) (Report, error) {
	if !trajectoryApproved() {
		return Report{}, fmt.Errorf("trajectory execution is blocked; set %s=1 in the trusted launcher environment", approvalEnv)
	}
	if !processIsolationSupported() {
		return Report{}, errProcessIsolationUnavailable
	}
	if err := input.Manifest.Validate(); err != nil {
		return Report{}, err
	}
	if err := input.Launcher.Validate(); err != nil {
		return Report{}, err
	}
	resolvedLauncher, launcher, err := resolveLauncher(input.Launcher)
	if err != nil {
		return Report{}, err
	}
	input.Launcher = resolvedLauncher
	processes := input.Processes
	if processes == nil {
		processes = ExecProcessRunner{}
	}
	now := input.Now
	if now == nil {
		now = time.Now
	}
	if input.RunID == "" {
		input.RunID = ids.New("run")
	}
	if !runIDPattern.MatchString(input.RunID) {
		return Report{}, errors.New("run id must be a stable identifier")
	}
	stateRoot := input.StateRoot
	if stateRoot == "" {
		stateRoot = filepath.Join(config.StateHome(), "trajectories")
	}
	stateRoot, err = canonicalPath(stateRoot)
	if err != nil {
		return Report{}, err
	}
	runRoot := filepath.Join(stateRoot, input.Manifest.ID, input.RunID)
	if withinPath(input.Manifest.RepositoryPath(), runRoot) || withinPath(runRoot, input.Manifest.RepositoryPath()) {
		return Report{}, errors.New("trajectory output must be outside the repository fixture")
	}
	if repositoryRoot := containingRepository(input.Manifest.baseDir); repositoryRoot != "" && withinPath(repositoryRoot, runRoot) {
		return Report{}, errors.New("trajectory output must be outside the scenario repository and public docs")
	}
	if err := createRunRoot(runRoot, input.RunID); err != nil {
		return Report{}, err
	}
	fixtureBaseline := filepath.Join(runRoot, "fixture-baseline")
	if err := copyFixture(input.Manifest.RepositoryPath(), fixtureBaseline); err != nil {
		return Report{}, fmt.Errorf("freeze repository fixture: %w", err)
	}
	baselineDigest, err := TreeDigest(fixtureBaseline)
	if err != nil {
		return Report{}, fmt.Errorf("digest frozen repository fixture: %w", err)
	}
	if baselineDigest != input.Manifest.Repository.Digest {
		return Report{}, fmt.Errorf("frozen repository fixture digest %s does not match manifest %s", baselineDigest, input.Manifest.Repository.Digest)
	}
	manifestDigest, err := semanticDigest(input.Manifest)
	if err != nil {
		return Report{}, err
	}
	oracleDigest, err := snapshotOracleSpecs(runRoot, &input.Manifest)
	if err != nil {
		return Report{}, err
	}
	configDigest, err := snapshotCortexConfig(runRoot)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		SchemaVersion: 1, RunID: input.RunID, ScenarioID: input.Manifest.ID,
		GeneratedAt: now().UTC(), ManifestDigest: manifestDigest,
		RepositoryDigest: input.Manifest.Repository.Digest, OracleDigest: oracleDigest,
		CortexConfigDigest: configDigest, Launcher: launcher,
		Harness: harnessProvenance(), Model: input.Manifest.Model,
	}
	if err := writeProvenance(runRoot, input.Manifest, launcher); err != nil {
		return Report{}, err
	}
	for _, arm := range input.Manifest.Arms {
		armReport, err := runArm(ctx, processes, input, runRoot, fixtureBaseline, launcher, oracleDigest, configDigest, arm)
		if err != nil {
			armReport.Error = err.Error()
			if armReport.Status == "" || armReport.Status == RunCompleted {
				armReport.Status = RunFailed
			}
		}
		report.Arms = append(report.Arms, armReport)
	}
	comparisons, scoreWarnings, err := ScoreArms(input.Manifest, report.Arms)
	if err != nil {
		report.Warnings = append(report.Warnings, "score comparison unavailable: "+err.Error())
	} else {
		report.Comparisons = comparisons
		report.Warnings = append(report.Warnings, scoreWarnings...)
	}
	path, err := writeReport(runRoot, report)
	if err != nil {
		return report, err
	}
	report.ReportPath = path
	// Rewrite once so the durable report carries its own relative location.
	if _, err := writeReport(runRoot, report); err != nil {
		return report, err
	}
	return report, nil
}

func semanticDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", digest), nil
}

func snapshotOracleSpecs(runRoot string, manifest *Manifest) (string, error) {
	oracleRoot := filepath.Join(runRoot, "oracle-specs")
	for _, spec := range manifest.Oracle.GlyphrunSpecs {
		source := filepath.Join(manifest.baseDir, filepath.FromSlash(spec.Path))
		if err := validatePathComponents(manifest.baseDir, spec.Path, false); err != nil {
			return "", err
		}
		data, err := readBoundedFile(source, maxOracleSpecBytes)
		if err != nil {
			return "", err
		}
		target := filepath.Join(oracleRoot, filepath.FromSlash(spec.Path))
		if err := secureMkdir(filepath.Dir(target)); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return "", err
		}
	}
	manifest.oracleRoot = oracleRoot
	return oracleSnapshotDigest(*manifest)
}

func oracleSnapshotDigest(manifest Manifest) (string, error) {
	specDigests := map[string]string{}
	root := manifest.oracleRoot
	for _, spec := range manifest.Oracle.GlyphrunSpecs {
		if err := validatePathComponents(root, spec.Path, false); err != nil {
			return "", err
		}
		data, err := readBoundedFile(filepath.Join(root, filepath.FromSlash(spec.Path)), maxOracleSpecBytes)
		if err != nil {
			return "", err
		}
		digest := sha256.Sum256(data)
		specDigests[spec.Path] = fmt.Sprintf("sha256:%x", digest)
	}
	return semanticDigest(struct {
		Oracle      Oracle            `json:"oracle"`
		SpecDigests map[string]string `json:"specDigests"`
	}{Oracle: manifest.Oracle, SpecDigests: specDigests})
}

func snapshotCortexConfig(runRoot string) (string, error) {
	targetDir := cortexConfigBaseline(runRoot)
	if err := secureMkdir(targetDir); err != nil {
		return "", err
	}
	source := filepath.Join(config.ConfigDir(), "config.yaml")
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return cortexConfigDigest(targetDir)
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("global Cortex config must be a regular file, not a symlink")
	}
	if info.Size() > maxManifestBytes {
		return "", fmt.Errorf("global Cortex config exceeds the %d-byte limit", maxManifestBytes)
	}
	target := filepath.Join(targetDir, "config.yaml")
	if err := copyFixtureFile(context.Background(), source, target, maxManifestBytes); err != nil {
		return "", fmt.Errorf("snapshot global Cortex config: %w", err)
	}
	if err := os.Chmod(target, 0o400); err != nil {
		return "", err
	}
	return cortexConfigDigest(targetDir)
}

func resolveLauncher(config LauncherConfig) (LauncherConfig, LauncherProvenance, error) {
	digest, _ := semanticDigest(config)
	redactor := redact.New()
	provenance := LauncherProvenance{ConfigDigest: digest, Argv: make([]string, len(config.Argv))}
	for index, arg := range config.Argv {
		provenance.Argv[index] = redactor.String(arg)
	}
	resolved, err := filepath.EvalSymlinks(config.Argv[0])
	if err != nil {
		return LauncherConfig{}, provenance, fmt.Errorf("resolve launcher executable: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return LauncherConfig{}, provenance, fmt.Errorf("resolve launcher executable: %w", err)
	}
	provenance.ResolvedPath = redactor.String(resolved)
	digest, err = regularFileDigest(resolved)
	if err != nil {
		return LauncherConfig{}, provenance, fmt.Errorf("digest launcher executable: %w", err)
	}
	provenance.BinaryDigest = digest
	resolvedConfig := config
	resolvedConfig.Argv = append([]string(nil), config.Argv...)
	resolvedConfig.Argv[0] = resolved
	return resolvedConfig, provenance, nil
}

func regularFileDigest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("executable is not a regular file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("executable does not have an execute bit")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func validateToolchainProvenance(toolchain []ToolchainProvenance, arm Arm, mutableRunRoot string) ([]ToolchainProvenance, error) {
	required := map[string]bool{}
	switch arm {
	case ArmRawTools:
	case ArmCortex:
		required["cortex"] = true
	case ArmCortexBob:
		required["cortex"] = true
		required["bob"] = true
	case ArmCortexBobLocalAgent:
		required["cortex"] = true
		required["bob"] = true
		required["local-agent"] = true
	default:
		return nil, fmt.Errorf("unsupported trajectory arm %q", arm)
	}
	managed := map[string]bool{"cortex": true, "bob": true, "local-agent": true}
	seen := make(map[string]bool, len(toolchain))
	validated := make([]ToolchainProvenance, 0, len(toolchain))
	for _, item := range toolchain {
		resolved, err := filepath.EvalSymlinks(item.ExecutablePath)
		if err != nil {
			return nil, fmt.Errorf("resolve toolchain %q executable: %w", item.Name, err)
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return nil, fmt.Errorf("resolve toolchain %q executable: %w", item.Name, err)
		}
		if withinPath(mutableRunRoot, resolved) {
			return nil, fmt.Errorf("toolchain %q executable must be outside the mutable trajectory run", item.Name)
		}
		digest, err := regularFileDigest(resolved)
		if err != nil {
			return nil, fmt.Errorf("digest toolchain %q executable: %w", item.Name, err)
		}
		if digest != item.BinaryDigest {
			return nil, fmt.Errorf("toolchain %q binary digest does not match its executable", item.Name)
		}
		if managed[item.Name] && !required[item.Name] {
			return nil, fmt.Errorf("toolchain %q is not permitted in arm %q", item.Name, arm)
		}
		item.ExecutablePath = resolved
		validated = append(validated, item)
		seen[item.Name] = true
	}
	for name := range required {
		if !seen[name] {
			return nil, fmt.Errorf("arm %q toolchain is missing required executable %q", arm, name)
		}
	}
	sort.Slice(validated, func(i, j int) bool { return validated[i].Name < validated[j].Name })
	return validated, nil
}

func writeProvenance(runRoot string, manifest Manifest, launcher LauncherProvenance) error {
	if err := writePrivateJSON(filepath.Join(runRoot, "manifest.json"), manifest); err != nil {
		return err
	}
	return writePrivateJSON(filepath.Join(runRoot, "launcher.json"), launcher)
}

func writePrivateJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func runArm(ctx context.Context, processes ProcessRunner, input RunInput, runRoot, fixtureBaseline string, launcher LauncherProvenance, expectedOracleDigest, expectedConfigDigest string, arm Arm) (ArmReport, error) {
	started := time.Now()
	armCtx, cancel := context.WithTimeout(ctx, input.Manifest.Budget.MaxWallTime.Value())
	defer cancel()
	report := ArmReport{Arm: arm, OracleIntegrity: true}
	workspace := filepath.Join(runRoot, "workspaces", string(arm))
	if err := verifyFixtureDigest(fixtureBaseline, input.Manifest.Repository.Digest); err != nil {
		report.OracleIntegrity = false
		return report, err
	}
	if err := copyFixture(fixtureBaseline, workspace); err != nil {
		return report, err
	}
	digest, err := TreeDigest(workspace)
	if err != nil || digest != input.Manifest.Repository.Digest {
		return report, fmt.Errorf("isolated workspace digest mismatch: got %s: %w", digest, err)
	}
	if err := initializeGit(armCtx, workspace); err != nil {
		return report, err
	}
	before, err := snapshotFiles(armCtx, workspace)
	if err != nil {
		return report, err
	}
	request := LauncherRequest{
		SchemaVersion: ProtocolSchemaVersion, Arm: arm, Workspace: workspace,
		Goal: input.Manifest.Goal, Acceptance: input.Manifest.Acceptance,
		Surfaces: input.Manifest.Surfaces, VerificationTargets: verificationTargets(input.Manifest),
		Model: input.Manifest.Model, Budget: input.Manifest.Budget,
	}
	stdin, digest, err := encodeLauncherRequest(request)
	if err != nil {
		return report, err
	}
	report.RequestDigest = digest
	launcherEnvironment, launcherConfigDigest, err := isolatedArmEnvironment(runRoot, "launcher", arm, expectedConfigDigest)
	if err != nil {
		return report, err
	}
	report.LauncherConfigDigest = launcherConfigDigest
	report.RuntimeRoot = filepath.ToSlash(filepath.Join("runtime", "launcher", string(arm)))
	launcherDigest, err := regularFileDigest(input.Launcher.Argv[0])
	if err != nil {
		return report, fmt.Errorf("revalidate launcher executable: %w", err)
	}
	if launcherDigest != launcher.BinaryDigest {
		return report, errors.New("launcher executable changed after its provenance was recorded")
	}
	launcherStarted := time.Now()
	processResult := processes.Run(armCtx, ProcessRequest{
		Kind: ProcessLauncher, Arm: arm, ID: "launcher", Argv: input.Launcher.Argv,
		Dir: workspace, Stdin: stdin, Timeout: input.Manifest.Budget.MaxWallTime.Value(),
		MaxStdout: maxLauncherStdout, MaxStderr: input.Manifest.Budget.MaxTraceBytes,
		Environment: launcherEnvironment, ExpectedBinaryDigest: launcher.BinaryDigest,
	})
	report.LatencyMs = time.Since(launcherStarted).Milliseconds()
	report.Trace, err = writeTrace(runRoot, arm, processResult.Stderr, processResult.StderrTruncated)
	if err != nil {
		return report, err
	}
	launcherConfigStable := true
	if err := verifyCortexConfigUse(launcherEnvironment["CORTEX_CONFIG_DIR"], expectedConfigDigest); err != nil {
		launcherConfigStable = false
		report.Error = joinError(report.Error, "launcher Cortex config changed during execution: "+err.Error())
		report.Warnings = append(report.Warnings, "launcher result is invalidated because its private Cortex configuration changed during execution")
	}
	launcherResult := LauncherResult{Status: RunIncomplete}
	launcherContractValid := false
	switch {
	case processResult.TimedOut:
		report.Status = RunTimeout
		report.Error = "launcher timed out"
	case processResult.Canceled:
		report.Status = RunIncomplete
		report.Error = "launcher canceled"
	case processResult.StdoutTruncated:
		report.Status = RunFailed
		report.Error = "launcher stdout exceeded the protocol limit"
	case processResult.Err != nil:
		report.Status = RunFailed
		if processResult.ExitCode == -1 {
			report.Status = RunBlocked
		}
		report.Error = processResult.Err.Error()
	default:
		decoded, decodeErr := decodeLauncherResult(processResult.Stdout, digest)
		var toolchain []ToolchainProvenance
		if decodeErr == nil {
			toolchain, decodeErr = validateLauncherResultForManifest(decoded, input.Manifest, arm, runRoot)
		}
		if decodeErr != nil {
			report.Status = RunFailed
			report.Error = decodeErr.Error()
		} else {
			launcherResult = decoded
			launcherContractValid = launcherConfigStable
			report.Status = decoded.Status
			report.Toolchain = toolchain
			report.ToolchainValidated = true
			report.ReportedCompletion = decoded.ReportedCompletion
			report.SelectedVerifiers = append([]string(nil), decoded.SelectedVerifiers...)
			report.InputTokens = decoded.InputTokens
			report.OutputTokens = decoded.OutputTokens
			report.EstimatedCostMicros = decoded.EstimatedCostMicros
			report.HumanInterventions = decoded.HumanInterventions
			report.ToolCalls = decoded.ToolCalls
			if !launcherConfigStable {
				report.Status = RunFailed
			}
		}
	}
	after, snapshotErr := snapshotFiles(armCtx, workspace)
	if snapshotErr != nil {
		return report, snapshotErr
	}
	report.ChangedFiles = changedFiles(before, after)
	report.WrongFiles = wrongFiles(report.ChangedFiles, input.Manifest.AllowedChanges)
	report.ScopeDrift = len(report.WrongFiles) > 0
	report.ProtectedChanges = protectedChanges(report.ChangedFiles, input.Manifest.Oracle.ProtectedPaths)
	report.OracleIntegrity = launcherContractValid && !report.ScopeDrift && len(report.ProtectedChanges) == 0
	if !report.OracleIntegrity {
		report.Warnings = append(report.Warnings, "oracle success is invalidated because the arm contract/toolchain, change scope, or oracle-protected dependencies are not trustworthy")
	}
	report.CorrectVerifierSelection = containsAll(report.SelectedVerifiers, oracleIDs(input.Manifest))
	current, revisionErr := adapters.NewGit().CurrentRevision(armCtx, workspace)
	if revisionErr != nil {
		report.Error = joinError(report.Error, "final revision unavailable: "+revisionErr.Error())
	}
	oracleWorkspace := filepath.Join(runRoot, "oracle-workspaces", string(arm))
	if err := verifyFixtureDigest(fixtureBaseline, input.Manifest.Repository.Digest); err != nil {
		report.OracleIntegrity = false
		return report, err
	}
	if err := buildOracleWorkspace(armCtx, fixtureBaseline, workspace, oracleWorkspace, input.Manifest.Repository.Digest, report.ChangedFiles, input.Manifest.AllowedChanges); err != nil {
		return report, fmt.Errorf("isolate oracle workspace: %w", err)
	}
	oracleEnvironment, oracleConfigDigest, err := isolatedArmEnvironment(runRoot, "oracle", arm, expectedConfigDigest)
	if err != nil {
		return report, err
	}
	report.OracleConfigDigest = oracleConfigDigest
	oracleInvocations, oracleTools, oracleWarnings := prepareOracleInvocations(input.Manifest, oracleWorkspace)
	report.OracleTools = oracleTools
	report.Warnings = append(report.Warnings, oracleWarnings...)
	oracleDigest, err := oracleSnapshotDigest(input.Manifest)
	if err != nil {
		report.OracleIntegrity = false
		return report, fmt.Errorf("verify frozen oracle specs: %w", err)
	}
	if oracleDigest != expectedOracleDigest {
		report.OracleIntegrity = false
		return report, fmt.Errorf("frozen oracle digest %s does not match expected %s", oracleDigest, expectedOracleDigest)
	}
	baselineProtectedDigest, err := protectedFilesDigest(fixtureBaseline, input.Manifest.Oracle.ProtectedPaths)
	if err != nil {
		report.OracleIntegrity = false
		return report, fmt.Errorf("digest frozen oracle dependencies: %w", err)
	}
	oracleProtectedDigest, err := protectedFilesDigest(oracleWorkspace, input.Manifest.Oracle.ProtectedPaths)
	if err != nil {
		report.OracleIntegrity = false
		return report, fmt.Errorf("digest isolated oracle dependencies: %w", err)
	}
	if oracleProtectedDigest != baselineProtectedDigest {
		report.OracleIntegrity = false
		return report, errors.New("isolated oracle dependencies do not match the frozen baseline")
	}
	oracleStarted := time.Now()
	report.Oracle = runOracles(armCtx, processes, oracleInvocations, input.Manifest.Budget.MaxOracleWallTime.Value(), oracleWorkspace, arm, oracleEnvironment)
	report.OracleLatencyMs = time.Since(oracleStarted).Milliseconds()
	if err := verifyCortexConfigUse(oracleEnvironment["CORTEX_CONFIG_DIR"], expectedConfigDigest); err != nil {
		report.OracleIntegrity = false
		report.Warnings = append(report.Warnings, "oracle success is invalidated because its private Cortex configuration changed during execution")
	}
	postOracleProtectedDigest, protectedErr := protectedFilesDigest(oracleWorkspace, input.Manifest.Oracle.ProtectedPaths)
	if protectedErr != nil || postOracleProtectedDigest != oracleProtectedDigest {
		report.OracleIntegrity = false
		report.Warnings = append(report.Warnings, "oracle success is invalidated because an oracle process changed a protected dependency")
	}
	report.OracleSuccess = report.OracleIntegrity && oracleSucceeded(report.Oracle)
	report.ExpectedCompletion = expectedCompletion(report.Oracle, report.OracleIntegrity)
	report.CompletionComparable = report.ReportedCompletion != ""
	report.HonestCompletion = report.CompletionComparable && report.ReportedCompletion == report.ExpectedCompletion
	receiptOracle := report.Oracle
	if !report.OracleIntegrity {
		receiptOracle = invalidatePassingOracleResults(report.Oracle)
	}
	report.CorrectReceipts, report.FalsePasses, report.StalePasses = receiptIntegrity(launcherResult.Receipts, receiptOracle, current, revisionErr)
	report.TotalLatencyMs = time.Since(started).Milliseconds()
	report.Observation = deriveObservation(report, launcherResult.Observation, len(input.Manifest.Acceptance))
	if report.Error != "" {
		return report, errors.New(report.Error)
	}
	return report, nil
}

func harnessProvenance() HarnessProvenance {
	provenance := HarnessProvenance{
		CortexVersion: version.Version, CortexCommit: version.Commit, CortexDate: version.Date,
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	}
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				provenance.VCSRevision = setting.Value
			case "vcs.modified":
				provenance.VCSModified = setting.Value == "true"
			}
		}
	}
	return provenance
}

func deriveObservation(report ArmReport, instrumented LauncherObservation, acceptanceCount int) baseeval.TrialObservation {
	observation := baseeval.TrialObservation{
		Evidence: instrumented.Evidence,
		Disproof: instrumented.Disproof,
		Recovery: instrumented.Recovery,
	}
	observation.Scope = baseeval.ScopeObservation{
		Required: true, BoundaryDeclared: instrumented.BoundaryDeclared, ChangedFiles: len(report.ChangedFiles),
		WithinBoundary: len(report.ChangedFiles) - len(report.WrongFiles), ScopeDriftDetected: report.ScopeDrift,
	}
	observation.Verifier = baseeval.VerifierObservation{
		Required: true, Claims: acceptanceCount,
		CorrectReceipts: report.CorrectReceipts,
		FalsePasses:     report.FalsePasses, StalePasses: report.StalePasses,
	}
	if report.CompletionComparable {
		observation.Completion = baseeval.CompletionObservation{
			Expected: report.ExpectedCompletion, Reported: report.ReportedCompletion,
		}
	}
	observation.Cost.ToolCalls = report.ToolCalls
	observation.Cost.LatencyMs = report.LatencyMs
	if report.EstimatedCostMicros != nil {
		observation.Cost.EstimatedCostMicros = *report.EstimatedCostMicros
	}
	return observation
}

func trajectoryApproved() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(approvalEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func initializeGit(ctx context.Context, workspace string) error {
	commands := [][]string{
		{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "init", "-q", "-b", "main"},
		{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "add", "-A"},
		{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "commit", "-qm", "scenario fixture"},
	}
	for _, args := range commands {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workspace
		cmd.Env = hermeticGitEnvironment()
		configureProcessGroup(cmd)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("initialize trajectory git repository: %w (%s)", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func hermeticGitEnvironment() []string {
	environment := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(os.TempDir(), "cortex-trajectory-git-home"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=cortex trajectory",
		"GIT_AUTHOR_EMAIL=trajectory@cortex.local",
		"GIT_COMMITTER_NAME=cortex trajectory",
		"GIT_COMMITTER_EMAIL=trajectory@cortex.local",
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
		"LC_ALL=C",
	}
	if temp := os.Getenv("TMPDIR"); temp != "" {
		environment = append(environment, "TMPDIR="+temp)
	}
	return environment
}

func copyFixture(source, destination string) error {
	return copyFixtureWithLimits(source, destination, defaultFixtureLimits)
}

func buildOracleWorkspace(ctx context.Context, baseline, armWorkspace, oracleWorkspace, expectedBaselineDigest string, changed, allowed []string) error {
	if err := copyFixture(baseline, oracleWorkspace); err != nil {
		return err
	}
	if err := verifyFixtureDigest(oracleWorkspace, expectedBaselineDigest); err != nil {
		return fmt.Errorf("verify oracle baseline: %w", err)
	}
	if err := initializeGit(ctx, oracleWorkspace); err != nil {
		return err
	}
	for _, relative := range changed {
		if !pathMatchesAny(relative, allowed) {
			continue
		}
		source := filepath.Join(armWorkspace, filepath.FromSlash(relative))
		target := filepath.Join(oracleWorkspace, filepath.FromSlash(relative))
		info, err := os.Lstat(source)
		switch {
		case errors.Is(err, os.ErrNotExist):
			targetInfo, targetErr := os.Lstat(target)
			if errors.Is(targetErr, os.ErrNotExist) {
				continue
			}
			if targetErr != nil {
				return targetErr
			}
			if targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.Mode().IsRegular() {
				return fmt.Errorf("oracle workspace target %q is not a regular file", relative)
			}
			if err := os.Remove(target); err != nil {
				return err
			}
		case err != nil:
			return err
		case info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular():
			return fmt.Errorf("allowed change %q is not a regular file", relative)
		default:
			if err := copyFixtureFile(ctx, source, target, maxFixtureFileBytes); err != nil {
				return err
			}
		}
	}
	_, err := snapshotFiles(ctx, oracleWorkspace)
	return err
}

func verifyFixtureDigest(root, expected string) error {
	digest, err := TreeDigest(root)
	if err != nil {
		return fmt.Errorf("verify frozen repository fixture: %w", err)
	}
	if digest != expected {
		return fmt.Errorf("frozen repository fixture digest %s does not match expected %s", digest, expected)
	}
	return nil
}

func snapshotFiles(ctx context.Context, root string) (map[string]string, error) {
	files := map[string]string{}
	var totalBytes int64
	entries := 0
	pathBytes := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries++
		if entries > maxFixtureEntries {
			return fmt.Errorf("workspace snapshot exceeds the %d-entry limit", maxFixtureEntries)
		}
		pathBytes += len(filepath.ToSlash(rel))
		if pathBytes > maxFixturePathBytes {
			return fmt.Errorf("workspace snapshot paths exceed the %d-byte aggregate limit", maxFixturePathBytes)
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace path %q became a symlink", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("workspace path %q is not a regular file", path)
		}
		if info.Size() > maxSnapshotFile {
			return fmt.Errorf("workspace file %q exceeds the %d-byte snapshot limit", path, maxSnapshotFile)
		}
		if info.Size() > maxSnapshotTotal-totalBytes {
			return fmt.Errorf("workspace snapshot exceeds the %d-byte total limit", maxSnapshotTotal)
		}
		digest, size, err := snapshotFileDigest(ctx, path, info, maxSnapshotTotal-totalBytes)
		if err != nil {
			return err
		}
		totalBytes += size
		files[filepath.ToSlash(rel)] = info.Mode().Perm().String() + ":" + digest
		return nil
	})
	return files, err
}

func snapshotFileDigest(ctx context.Context, path string, expected os.FileInfo, remainingTotal int64) (digest string, size int64, err error) {
	file, err := openSnapshotFile(path)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			digest, size, err = "", 0, closeErr
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		return "", 0, fmt.Errorf("workspace path %q changed before it could be snapshotted", path)
	}
	limit := int64(maxSnapshotFile)
	if remainingTotal < limit {
		limit = remainingTotal
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	reader := io.LimitReader(file, limit+1)
	for {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		read, readErr := reader.Read(buffer)
		if read > 0 {
			size += int64(read)
			if size > limit {
				if limit == maxSnapshotFile {
					return "", 0, fmt.Errorf("workspace file %q exceeds the %d-byte snapshot limit", path, maxSnapshotFile)
				}
				return "", 0, fmt.Errorf("workspace snapshot exceeds the %d-byte total limit", maxSnapshotTotal)
			}
			if _, err := hash.Write(buffer[:read]); err != nil {
				return "", 0, err
			}
		}
		switch {
		case errors.Is(readErr, io.EOF):
			final, err := file.Stat()
			if err != nil {
				return "", 0, err
			}
			if !os.SameFile(opened, final) || final.Size() != opened.Size() || final.ModTime() != opened.ModTime() {
				return "", 0, fmt.Errorf("workspace file %q changed while it was snapshotted", path)
			}
			return fmt.Sprintf("%x", hash.Sum(nil)), size, nil
		case readErr != nil:
			return "", 0, readErr
		}
	}
}

func changedFiles(before, after map[string]string) []string {
	set := map[string]bool{}
	for path, digest := range before {
		if after[path] != digest {
			set[path] = true
		}
	}
	for path, digest := range after {
		if before[path] != digest {
			set[path] = true
		}
	}
	result := make([]string, 0, len(set))
	for path := range set {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func wrongFiles(changed, allowed []string) []string {
	var wrong []string
	for _, changedPath := range changed {
		if !pathMatchesAny(changedPath, allowed) {
			wrong = append(wrong, changedPath)
		}
	}
	return wrong
}

func pathMatchesAny(relative string, patterns []string) bool {
	for _, pattern := range patterns {
		if matched, _ := pathpkg.Match(pattern, relative); matched {
			return true
		}
	}
	return false
}

func protectedChanges(changed, protected []string) []string {
	set := make(map[string]bool, len(protected))
	for _, relative := range protected {
		set[relative] = true
	}
	var result []string
	for _, relative := range changed {
		if set[relative] {
			result = append(result, relative)
		}
	}
	return result
}

func oracleSucceeded(results []OracleResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, result := range results {
		if result.Status != OraclePassed {
			return false
		}
	}
	return true
}

func invalidatePassingOracleResults(results []OracleResult) []OracleResult {
	invalidated := append([]OracleResult(nil), results...)
	for index := range invalidated {
		if invalidated[index].Status == OraclePassed {
			invalidated[index].Status = OracleInconclusive
		}
	}
	return invalidated
}

func expectedCompletion(results []OracleResult, integrity bool) baseeval.CompletionLabel {
	if integrity && oracleSucceeded(results) {
		return baseeval.CompletionVerified
	}
	for _, result := range results {
		if result.Status == OracleFailed {
			return baseeval.CompletionFailed
		}
	}
	for _, result := range results {
		if result.Status == OracleTimeout {
			return baseeval.CompletionIncomplete
		}
	}
	return baseeval.CompletionUnverified
}

func verificationTargets(manifest Manifest) []VerificationTarget {
	targets := make([]VerificationTarget, 0, len(manifest.Oracle.Commands)+len(manifest.Oracle.GlyphrunSpecs))
	for _, command := range manifest.Oracle.Commands {
		targets = append(targets, VerificationTarget{ID: command.ID, Kind: "command", ClaimIDs: append([]string(nil), command.ClaimIDs...)})
	}
	for _, spec := range manifest.Oracle.GlyphrunSpecs {
		targets = append(targets, VerificationTarget{ID: spec.ID, Kind: "glyphrun", ClaimIDs: append([]string(nil), spec.ClaimIDs...)})
	}
	return targets
}

func oracleIDs(manifest Manifest) []string {
	ids := make([]string, 0, len(manifest.Oracle.Commands)+len(manifest.Oracle.GlyphrunSpecs))
	for _, command := range manifest.Oracle.Commands {
		ids = append(ids, command.ID)
	}
	for _, spec := range manifest.Oracle.GlyphrunSpecs {
		ids = append(ids, spec.ID)
	}
	return ids
}

func containsAll(got, want []string) bool {
	set := map[string]bool{}
	for _, value := range got {
		set[value] = true
	}
	for _, value := range want {
		if !set[value] {
			return false
		}
	}
	return true
}

func receiptIntegrity(receipts []ReceiptObservation, oracles []OracleResult, current adapters.Revision, revisionErr error) (correctReceipts, falsePasses, stalePasses int) {
	type verifierClaim struct {
		verifierID string
		claimID    string
	}
	oracleByPair := map[verifierClaim]OracleStatus{}
	requiredByClaim := map[string][]verifierClaim{}
	for _, oracle := range oracles {
		for _, claimID := range oracle.ClaimIDs {
			pair := verifierClaim{verifierID: oracle.ID, claimID: claimID}
			oracleByPair[pair] = oracle.Status
			requiredByClaim[claimID] = append(requiredByClaim[claimID], pair)
		}
	}
	correctPairs := map[verifierClaim]bool{}
	falseClaims := map[string]bool{}
	staleClaims := map[string]bool{}
	for _, receipt := range receipts {
		for _, claimID := range receipt.ClaimIDs {
			pair := verifierClaim{verifierID: receipt.VerifierID, claimID: claimID}
			oracleStatus, known := oracleByPair[pair]
			if !known {
				continue
			}
			if receipt.Status != "passed" {
				if oracleStatus != OraclePassed {
					correctPairs[pair] = true
				}
				continue
			}
			isFalse := oracleStatus != OraclePassed
			isStale := revisionErr != nil || receipt.Revision == "" || receipt.DirtyDigest == "" || receipt.Revision != current.Commit || receipt.DirtyDigest != current.DirtyDigest
			if isFalse {
				falseClaims[claimID] = true
			}
			if isStale {
				staleClaims[claimID] = true
			}
			if !isFalse && !isStale {
				correctPairs[pair] = true
			}
		}
	}
	correctClaims := map[string]bool{}
	for claimID, requiredPairs := range requiredByClaim {
		complete := len(requiredPairs) > 0
		for _, pair := range requiredPairs {
			if !correctPairs[pair] {
				complete = false
				break
			}
		}
		if complete {
			correctClaims[claimID] = true
		}
	}
	return len(correctClaims), len(falseClaims), len(staleClaims)
}

func secureMkdir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func cortexConfigBaseline(runRoot string) string {
	return filepath.Join(runRoot, "runtime", "cortex-config-baseline")
}

func cortexConfigDigest(directory string) (string, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("cortex config root must be a real directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return semanticDigest(struct{}{})
	}
	if len(entries) != 1 || entries[0].Name() != "config.yaml" {
		return "", errors.New("cortex config root contains an unexpected entry")
	}
	path := filepath.Join(directory, "config.yaml")
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if fileInfo.Mode()&os.ModeSymlink != 0 || !fileInfo.Mode().IsRegular() {
		return "", errors.New("cortex config must be a regular file, not a symlink")
	}
	if fileInfo.Size() > maxManifestBytes {
		return "", fmt.Errorf("cortex config exceeds the %d-byte limit", maxManifestBytes)
	}
	hash := sha256.New()
	if _, err := streamFixtureFile(context.Background(), path, fileInfo, maxManifestBytes, hash); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func prepareCortexConfigUse(runRoot, target, expectedDigest string) (string, error) {
	baseline := cortexConfigBaseline(runRoot)
	digest, err := cortexConfigDigest(baseline)
	if err != nil {
		return "", fmt.Errorf("verify frozen Cortex config: %w", err)
	}
	if digest != expectedDigest {
		return "", fmt.Errorf("frozen Cortex config digest %s does not match expected %s", digest, expectedDigest)
	}
	if err := secureMkdir(target); err != nil {
		return "", err
	}
	source := filepath.Join(baseline, "config.yaml")
	if _, err := os.Lstat(source); err == nil {
		if err := copyFixtureFile(context.Background(), source, filepath.Join(target, "config.yaml"), maxManifestBytes); err != nil {
			return "", fmt.Errorf("copy frozen Cortex config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	useDigest, err := cortexConfigDigest(target)
	if err != nil {
		return "", fmt.Errorf("digest private Cortex config: %w", err)
	}
	if useDigest != expectedDigest {
		return "", fmt.Errorf("private Cortex config digest %s does not match expected %s", useDigest, expectedDigest)
	}
	// Recheck the source after copying so a concurrent baseline replacement
	// cannot silently bind the private use to a different configuration.
	if err := verifyCortexConfigUse(baseline, expectedDigest); err != nil {
		return "", fmt.Errorf("revalidate frozen Cortex config: %w", err)
	}
	return useDigest, nil
}

func verifyCortexConfigUse(directory, expectedDigest string) error {
	digest, err := cortexConfigDigest(directory)
	if err != nil {
		return err
	}
	if digest != expectedDigest {
		return fmt.Errorf("cortex config digest %s does not match expected %s", digest, expectedDigest)
	}
	return nil
}

func isolatedArmEnvironment(runRoot, phase string, arm Arm, expectedConfigDigest string) (map[string]string, string, error) {
	root := filepath.Join(runRoot, "runtime", phase, string(arm))
	configRoot := filepath.Join(root, "cortex-config")
	paths := map[string]string{
		"CORTEX_CONFIG_DIR": configRoot,
		"CORTEX_STATE_DIR":  filepath.Join(root, "cortex-state"),
		"CORTEX_CACHE_DIR":  filepath.Join(root, "cortex-cache"),
		"CORTEX_CASES_DIR":  filepath.Join(root, "cortex-cases"),
		"XDG_STATE_HOME":    filepath.Join(root, "xdg-state"),
		"XDG_CACHE_HOME":    filepath.Join(root, "xdg-cache"),
		"XDG_DATA_HOME":     filepath.Join(root, "xdg-data"),
	}
	for _, path := range paths {
		if err := secureMkdir(path); err != nil {
			return nil, "", err
		}
	}
	digest, err := prepareCortexConfigUse(runRoot, configRoot, expectedConfigDigest)
	if err != nil {
		return nil, "", err
	}
	return paths, digest, nil
}

func createRunRoot(path, runID string) error {
	parent := filepath.Dir(path)
	stateRoot := filepath.Dir(parent)
	if err := ensureDirectory(stateRoot); err != nil {
		return err
	}
	if info, err := os.Lstat(parent); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("trajectory scenario root must be a real directory")
		}
		if err := os.Chmod(parent, 0o700); err != nil {
			return err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(parent, 0o700); err != nil {
			return err
		}
	} else {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("trajectory run %q already exists", runID)
		}
		return err
	}
	return nil
}

func ensureDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("trajectory state root must be a real directory")
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	probe := abs
	var suffix []string
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("cannot resolve trajectory state root %q", path)
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
	resolved, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return "", err
	}
	for index := len(suffix) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, suffix[index])
	}
	return resolved, nil
}

func containingRepository(path string) string {
	current := filepath.Clean(path)
	for {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func withinPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func joinError(first, second string) string {
	if first == "" {
		return second
	}
	return first + "; " + second
}

func writeReport(runRoot string, report Report) (string, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	path := filepath.Join(runRoot, "report.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return "report.json", nil
}

func writeTrace(runRoot string, arm Arm, data []byte, truncated bool) (TraceRecord, error) {
	if truncated {
		return TraceRecord{Omitted: true, Truncated: true}, nil
	}
	if len(data) == 0 {
		return TraceRecord{}, nil
	}
	relative := filepath.Join("traces", string(arm)+".stderr.txt")
	path := filepath.Join(runRoot, relative)
	if err := secureMkdir(filepath.Dir(path)); err != nil {
		return TraceRecord{}, err
	}
	redacted := []byte(redact.New().String(string(data)))
	if err := os.WriteFile(path, redacted, 0o600); err != nil {
		return TraceRecord{}, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return TraceRecord{}, err
	}
	return TraceRecord{Path: filepath.ToSlash(relative)}, nil
}
