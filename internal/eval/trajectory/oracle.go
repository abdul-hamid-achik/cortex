package trajectory

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// This file prepares and runs the independent oracles that judge an arm's diff
// in a clean workspace — separate from the arm execution in runner.go.

type preparedOracle struct {
	id                   string
	argv                 []string
	claimIDs             []string
	timeout              time.Duration
	expectedBinaryDigest string
	preparationError     error
}

// prepareOracleInvocations preserves manifest order all the way from
// provenance to execution. Oracle IDs are validated as globally unique, but a
// positional plan also avoids an ID-keyed lookup silently binding the wrong
// argv if validation ever regresses.
func prepareOracleInvocations(manifest Manifest, workspace string) ([]preparedOracle, []ExecutableProvenance, []string) {
	targets := make([]preparedOracle, 0, len(manifest.Oracle.Commands)+len(manifest.Oracle.GlyphrunSpecs))
	for _, command := range manifest.Oracle.Commands {
		targets = append(targets, preparedOracle{
			id: command.ID, argv: append([]string(nil), command.Argv...),
			claimIDs: append([]string(nil), command.ClaimIDs...), timeout: command.Timeout.Value(),
		})
	}
	for _, spec := range manifest.Oracle.GlyphrunSpecs {
		targets = append(targets, preparedOracle{
			id:       spec.ID,
			argv:     []string{"glyph", "--format", "json", "run", manifest.GlyphrunPath(spec.Path)},
			claimIDs: append([]string(nil), spec.ClaimIDs...), timeout: spec.Timeout.Value(),
		})
	}
	provenance := make([]ExecutableProvenance, 0, len(targets))
	warnings := make([]string, 0)
	redactor := redact.New()
	for index := range targets {
		item, resolved, err := executableProvenance(targets[index].id, targets[index].argv[0], workspace)
		provenance = append(provenance, item)
		if err != nil {
			targets[index].preparationError = err
			warnings = append(warnings, redactor.String(fmt.Sprintf("%s oracle executable identity unavailable: %v", targets[index].id, err)))
			continue
		}
		targets[index].argv[0] = resolved
		targets[index].expectedBinaryDigest = item.BinaryDigest
	}
	return targets, provenance, warnings
}

func executableProvenance(id, argv0, workspace string) (ExecutableProvenance, string, error) {
	redactor := redact.New()
	provenance := ExecutableProvenance{ID: id, Argv0: redactor.String(argv0)}
	var candidate string
	var err error
	if filepath.IsAbs(argv0) {
		candidate = filepath.Clean(argv0)
	} else if strings.ContainsAny(argv0, `/\`) {
		candidate, err = filepath.Abs(filepath.Join(workspace, filepath.FromSlash(argv0)))
	} else {
		candidate, err = exec.LookPath(argv0)
	}
	if err != nil {
		return provenance, "", err
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return provenance, "", err
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return provenance, "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return provenance, "", err
	}
	provenance.ResolvedPath = redactor.String(resolved)
	provenance.BinaryDigest, err = regularFileDigest(resolved)
	if err != nil {
		return provenance, "", err
	}
	return provenance, resolved, nil
}

func runOracles(ctx context.Context, processes ProcessRunner, oracles []preparedOracle, maxWallTime time.Duration, workspace string, arm Arm, environment map[string]string) []OracleResult {
	oracleCtx, cancel := context.WithTimeout(ctx, maxWallTime)
	defer cancel()
	results := make([]OracleResult, 0, len(oracles))
	for _, oracle := range oracles {
		results = append(results, runOracle(oracleCtx, processes, arm, workspace, oracle, environment))
	}
	return results
}

func runOracle(ctx context.Context, processes ProcessRunner, arm Arm, workspace string, oracle preparedOracle, environment map[string]string) OracleResult {
	started := time.Now()
	result := OracleResult{ID: oracle.id, ClaimIDs: append([]string(nil), oracle.claimIDs...), ExitCode: -1}
	if err := ctx.Err(); err != nil {
		result.Status = OracleNotRun
		result.Message = "oracle canceled before execution"
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}
	if oracle.preparationError != nil {
		result.Status = OracleBlocked
		result.Message = "oracle executable identity unavailable: " + oracle.preparationError.Error()
		result.Message = redact.New().String(result.Message)
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}
	if len(oracle.argv) == 0 || !filepath.IsAbs(oracle.argv[0]) || oracle.expectedBinaryDigest == "" {
		result.Status = OracleBlocked
		result.Message = "oracle executable identity is incomplete"
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}
	digest, err := regularFileDigest(oracle.argv[0])
	if err != nil || digest != oracle.expectedBinaryDigest {
		result.Status = OracleBlocked
		if err != nil {
			result.Message = "revalidate oracle executable: " + err.Error()
		} else {
			result.Message = "oracle executable digest changed before execution"
		}
		result.Message = redact.New().String(result.Message)
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}
	process := processes.Run(ctx, ProcessRequest{
		Kind: ProcessOracle, Arm: arm, ID: oracle.id, Argv: oracle.argv, Dir: workspace,
		Timeout: oracle.timeout, MaxStdout: maxOracleOutput, MaxStderr: maxOracleOutput,
		Environment: environment, ExpectedBinaryDigest: oracle.expectedBinaryDigest,
	})
	result.ExitCode = process.ExitCode
	result.DurationMs = time.Since(started).Milliseconds()
	switch {
	case process.TimedOut:
		result.Status = OracleTimeout
		result.Message = "oracle timed out"
	case process.Canceled:
		result.Status = OracleNotRun
		result.Message = "oracle canceled"
	case process.StdoutTruncated || process.StderrTruncated:
		result.Status = OracleInconclusive
		result.Message = "oracle output exceeded the capture limit"
	case process.Err != nil && process.ExitCode == -1:
		result.Status = OracleBlocked
		result.Message = process.Err.Error()
	case process.Err != nil || process.ExitCode != 0:
		result.Status = OracleFailed
		result.Message = string(process.Stderr)
	default:
		result.Status = OraclePassed
	}
	result.Message = redact.New().String(result.Message)
	return result
}
