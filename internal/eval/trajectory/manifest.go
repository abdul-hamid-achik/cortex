// Package trajectory defines an opt-in, oracle-driven empirical evaluation
// harness. It is deliberately separate from Cortex's runtime kernel and from
// the deterministic calibration fixtures in the parent eval package.
package trajectory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"gopkg.in/yaml.v3"
)

const ManifestSchemaVersion = 1

const (
	maxManifestBytes     = 1 << 20
	maxOracleSpecBytes   = 1 << 20
	maxFixtureFileBytes  = 256 << 20
	maxFixtureTotalBytes = 1 << 30
	maxFixtureEntries    = 100_000
	maxFixturePathBytes  = 16 << 20
)

const (
	ArmRawTools            Arm = "raw_tools"
	ArmCortex              Arm = "cortex"
	ArmCortexBob           Arm = "cortex_bob"
	ArmCortexBobLocalAgent Arm = "cortex_bob_local_agent"
)

var (
	stableIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	runIDPattern    = regexp.MustCompile(`^(?:[a-z][a-z0-9_-]{0,127}|run_[0-9A-HJKMNP-TV-Z]{16})$`)
)

// Arm is one controlled tool-exposure condition.
type Arm string

// Duration is a strict YAML duration string with JSON string output.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return errors.New("duration must be a string")
	}
	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q", node.Value)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%q", time.Duration(d).String())), nil
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("duration must be a string")
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("invalid duration %q", value)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Value() time.Duration { return time.Duration(d) }

type Repository struct {
	Fixture string `yaml:"fixture" json:"fixture"`
	Digest  string `yaml:"digest" json:"digest"`
}

type Model struct {
	Identifier            string  `yaml:"identifier" json:"identifier"`
	Build                 string  `yaml:"build" json:"build"`
	Temperature           float64 `yaml:"temperature" json:"temperature"`
	Seed                  *int64  `yaml:"seed,omitempty" json:"seed,omitempty"`
	SeedUnsupportedReason string  `yaml:"seed_unsupported_reason,omitempty" json:"seedUnsupportedReason,omitempty"`
	ContextBudgetTokens   int     `yaml:"context_budget_tokens" json:"contextBudgetTokens"`
}

type Budget struct {
	MaxToolCalls           int      `yaml:"max_tool_calls" json:"maxToolCalls"`
	MaxWallTime            Duration `yaml:"max_wall_time" json:"maxWallTime"`
	MaxOracleWallTime      Duration `yaml:"max_oracle_wall_time" json:"maxOracleWallTime"`
	MaxTraceBytes          int      `yaml:"max_trace_bytes" json:"maxTraceBytes"`
	MaxEstimatedCostMicros int64    `yaml:"max_estimated_cost_micros" json:"maxEstimatedCostMicros"`
}

type OracleCommand struct {
	ID       string   `yaml:"id" json:"id"`
	Argv     []string `yaml:"argv" json:"argv"`
	ClaimIDs []string `yaml:"claim_ids" json:"claimIds"`
	Timeout  Duration `yaml:"timeout" json:"timeout"`
}

type GlyphrunSpec struct {
	ID       string   `yaml:"id" json:"id"`
	Path     string   `yaml:"path" json:"path"`
	ClaimIDs []string `yaml:"claim_ids" json:"claimIds"`
	Timeout  Duration `yaml:"timeout" json:"timeout"`
}

type Oracle struct {
	Commands       []OracleCommand `yaml:"commands" json:"commands"`
	GlyphrunSpecs  []GlyphrunSpec  `yaml:"glyphrun_specs" json:"glyphrunSpecs"`
	ProtectedPaths []string        `yaml:"protected_paths" json:"protectedPaths"`
}

// Manifest describes one fixed scenario. It cannot name a launcher, set an
// environment, or approve execution; those authorities live outside it.
type Manifest struct {
	SchemaVersion  int                          `yaml:"schema_version" json:"schemaVersion"`
	ID             string                       `yaml:"id" json:"id"`
	Repository     Repository                   `yaml:"repository" json:"repository"`
	Goal           string                       `yaml:"goal" json:"goal"`
	Acceptance     []domain.AcceptanceCriterion `yaml:"acceptance" json:"acceptance"`
	Surfaces       []domain.Surface             `yaml:"surfaces" json:"surfaces"`
	AllowedChanges []string                     `yaml:"allowed_changes" json:"allowedChanges"`
	Model          Model                        `yaml:"model" json:"model"`
	Arms           []Arm                        `yaml:"arms" json:"arms"`
	Budget         Budget                       `yaml:"budget" json:"budget"`
	Oracle         Oracle                       `yaml:"oracle" json:"oracle"`
	baseDir        string
	oracleRoot     string
}

// LoadManifest decodes one strict YAML document and validates its fixture
// digest without executing any scenario code.
func LoadManifest(path string) (Manifest, error) {
	data, err := readBoundedFile(path, maxManifestBytes)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := decodeStrictYAML(data, &manifest); err != nil {
		return Manifest{}, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Manifest{}, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Manifest{}, err
	}
	manifest.baseDir = filepath.Dir(resolved)
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	digest, err := TreeDigest(manifest.RepositoryPath())
	if err != nil {
		return Manifest{}, fmt.Errorf("repository fixture: %w", err)
	}
	if digest != manifest.Repository.Digest {
		return Manifest{}, fmt.Errorf("repository fixture digest %s does not match manifest %s", digest, manifest.Repository.Digest)
	}
	return manifest, nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("input must be a regular file")
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("input exceeds the %d-byte limit", limit)
	}
	return os.ReadFile(path)
}

func decodeStrictYAML(data []byte, value any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple YAML documents are not supported")
		}
		return err
	}
	return nil
}

// Validate enforces reproducibility, bounds, oracle coverage, and safe paths.
func (m Manifest) Validate() error {
	if m.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("unsupported trajectory manifest schema version %d", m.SchemaVersion)
	}
	if !stableIDPattern.MatchString(m.ID) {
		return errors.New("scenario id must be a stable lowercase identifier")
	}
	if strings.TrimSpace(m.Goal) == "" || len(m.Goal) > 16<<10 {
		return errors.New("scenario goal must be non-empty and at most 16384 bytes")
	}
	if err := domain.ValidateAcceptanceCriteria(m.Acceptance); err != nil {
		return fmt.Errorf("acceptance: %w", err)
	}
	if len(m.Acceptance) == 0 {
		return errors.New("scenario needs at least one acceptance criterion")
	}
	if len(m.Surfaces) == 0 {
		return errors.New("scenario needs at least one surface")
	}
	seenSurfaces := map[domain.Surface]bool{}
	for _, surface := range m.Surfaces {
		if !surface.Valid() || seenSurfaces[surface] {
			return fmt.Errorf("invalid or duplicate surface %q", surface)
		}
		seenSurfaces[surface] = true
	}
	if err := validateRelativePath(m.Repository.Fixture, "repository fixture"); err != nil {
		return err
	}
	if err := validatePathComponents(m.baseDir, m.Repository.Fixture, true); err != nil {
		return fmt.Errorf("repository fixture: %w", err)
	}
	if !digestPattern.MatchString(m.Repository.Digest) {
		return errors.New("repository digest must be sha256:<64 lowercase hex characters>")
	}
	if len(m.AllowedChanges) == 0 || len(m.AllowedChanges) > 256 {
		return errors.New("allowed_changes must contain between 1 and 256 paths")
	}
	seenPaths := map[string]bool{}
	for _, path := range m.AllowedChanges {
		if err := validateRelativePath(path, "allowed change"); err != nil {
			return err
		}
		if _, err := pathpkg.Match(path, "probe"); err != nil {
			return fmt.Errorf("allowed change %q is not a valid slash-separated pattern", path)
		}
		if seenPaths[path] {
			return fmt.Errorf("duplicate allowed change %q", path)
		}
		seenPaths[path] = true
	}
	if err := m.Model.validate(); err != nil {
		return err
	}
	if err := validateArms(m.Arms); err != nil {
		return err
	}
	if err := m.Budget.validate(); err != nil {
		return err
	}
	return m.validateOracle()
}

func (m Model) validate() error {
	if strings.TrimSpace(m.Identifier) == "" || strings.TrimSpace(m.Build) == "" {
		return errors.New("model identifier and build are required")
	}
	if math.IsNaN(m.Temperature) || math.IsInf(m.Temperature, 0) || m.Temperature < 0 || m.Temperature > 2 {
		return errors.New("model temperature must be finite and between 0 and 2")
	}
	if (m.Seed == nil) == (strings.TrimSpace(m.SeedUnsupportedReason) == "") {
		return errors.New("model must provide either a seed or a seed_unsupported_reason")
	}
	if m.ContextBudgetTokens < 1 || m.ContextBudgetTokens > 10_000_000 {
		return errors.New("model context_budget_tokens must be between 1 and 10000000")
	}
	return nil
}

func validateArms(arms []Arm) error {
	if len(arms) < 2 || len(arms) > 4 || arms[0] != ArmRawTools {
		return errors.New("arms must contain raw_tools first and between one and three candidate arms")
	}
	allowed := map[Arm]bool{
		ArmRawTools: true, ArmCortex: true, ArmCortexBob: true, ArmCortexBobLocalAgent: true,
	}
	seen := map[Arm]bool{}
	for _, arm := range arms {
		if !allowed[arm] || seen[arm] {
			return fmt.Errorf("invalid or duplicate trajectory arm %q", arm)
		}
		seen[arm] = true
	}
	return nil
}

func (b Budget) validate() error {
	if b.MaxToolCalls < 1 || b.MaxToolCalls > 10_000 {
		return errors.New("budget max_tool_calls must be between 1 and 10000")
	}
	if b.MaxWallTime.Value() <= 0 || b.MaxWallTime.Value() > 24*time.Hour {
		return errors.New("budget max_wall_time must be positive and at most 24h")
	}
	if b.MaxOracleWallTime.Value() <= 0 || b.MaxOracleWallTime.Value() > b.MaxWallTime.Value() {
		return errors.New("budget max_oracle_wall_time must be positive and no greater than max_wall_time")
	}
	if b.MaxTraceBytes < 1 || b.MaxTraceBytes > 64<<20 {
		return errors.New("budget max_trace_bytes must be between 1 and 67108864")
	}
	if b.MaxEstimatedCostMicros < 0 || b.MaxEstimatedCostMicros > 1_000_000_000 {
		return errors.New("budget max_estimated_cost_micros must be between 0 and 1000000000")
	}
	return nil
}

func (m Manifest) validateOracle() error {
	covered := map[string]bool{}
	acceptance := map[string]bool{}
	for _, criterion := range m.Acceptance {
		acceptance[criterion.ID] = true
	}
	seenChecks := map[string]bool{}
	if len(m.Oracle.ProtectedPaths) == 0 || len(m.Oracle.ProtectedPaths) > 256 {
		return errors.New("oracle protected_paths must contain between 1 and 256 fixture files")
	}
	seenProtected := map[string]bool{}
	for _, protected := range m.Oracle.ProtectedPaths {
		if err := validateRelativePath(protected, "oracle protected path"); err != nil {
			return err
		}
		if strings.ContainsAny(protected, `*?[\`) {
			return fmt.Errorf("oracle protected path %q must name an exact fixture file", protected)
		}
		if seenProtected[protected] {
			return fmt.Errorf("duplicate oracle protected path %q", protected)
		}
		seenProtected[protected] = true
		if err := validatePathComponentsWithLimit(m.RepositoryPath(), protected, false, maxFixtureFileBytes); err != nil {
			return fmt.Errorf("oracle protected path %q: %w", protected, err)
		}
		for _, allowed := range m.AllowedChanges {
			if matched, _ := pathpkg.Match(allowed, protected); matched {
				return fmt.Errorf("oracle protected path %q overlaps allowed change %q", protected, allowed)
			}
		}
	}
	check := func(id string, claimIDs []string, timeout Duration) error {
		if !stableIDPattern.MatchString(id) || seenChecks[id] {
			return fmt.Errorf("invalid or duplicate oracle id %q", id)
		}
		seenChecks[id] = true
		if timeout.Value() <= 0 || timeout.Value() > m.Budget.MaxOracleWallTime.Value() {
			return fmt.Errorf("oracle %q timeout must be positive and within max_oracle_wall_time", id)
		}
		if len(claimIDs) == 0 {
			return fmt.Errorf("oracle %q must cover at least one acceptance criterion", id)
		}
		for _, claimID := range claimIDs {
			if !acceptance[claimID] {
				return fmt.Errorf("oracle %q references unknown acceptance criterion %q", id, claimID)
			}
			covered[claimID] = true
		}
		return nil
	}
	for _, command := range m.Oracle.Commands {
		if len(command.Argv) == 0 || len(command.Argv) > 64 {
			return fmt.Errorf("oracle command %q must contain between 1 and 64 argv entries", command.ID)
		}
		if strings.TrimSpace(command.Argv[0]) == "" {
			return fmt.Errorf("oracle command %q executable cannot be empty", command.ID)
		}
		for _, arg := range command.Argv {
			if strings.ContainsRune(arg, 0) || len(arg) > 4<<10 {
				return fmt.Errorf("oracle command %q contains an invalid argv entry", command.ID)
			}
		}
		if relativeExecutable, ok := fixtureRelativeExecutable(command.Argv[0]); ok && !seenProtected[relativeExecutable] {
			return fmt.Errorf("oracle command %q executable %q must be listed in protected_paths", command.ID, relativeExecutable)
		}
		if err := check(command.ID, command.ClaimIDs, command.Timeout); err != nil {
			return err
		}
	}
	for _, spec := range m.Oracle.GlyphrunSpecs {
		if err := validateRelativePath(spec.Path, "glyphrun spec"); err != nil {
			return err
		}
		if err := validatePathComponents(m.baseDir, spec.Path, false); err != nil {
			return fmt.Errorf("glyphrun spec %q: %w", spec.ID, err)
		}
		if err := check(spec.ID, spec.ClaimIDs, spec.Timeout); err != nil {
			return err
		}
	}
	if len(seenChecks) == 0 {
		return errors.New("scenario needs at least one oracle")
	}
	for claimID := range acceptance {
		if !covered[claimID] {
			return fmt.Errorf("acceptance criterion %q is not covered by an oracle", claimID)
		}
	}
	return nil
}

func fixtureRelativeExecutable(argv0 string) (string, bool) {
	if filepath.IsAbs(argv0) || !strings.ContainsAny(argv0, `/\`) {
		return "", false
	}
	value := filepath.ToSlash(filepath.Clean(argv0))
	value = strings.TrimPrefix(value, "./")
	if err := validateRelativePath(value, "oracle command executable"); err != nil {
		return "", false
	}
	return value, true
}

func validatePathComponents(base, relative string, wantDirectory bool) error {
	return validatePathComponentsWithLimit(base, relative, wantDirectory, maxOracleSpecBytes)
}

func validatePathComponentsWithLimit(base, relative string, wantDirectory bool, maxBytes int64) error {
	current := base
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q is a symlink", filepath.ToSlash(strings.Join(parts[:index+1], string(filepath.Separator))))
		}
		if index < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("path component %q is not a directory", filepath.ToSlash(strings.Join(parts[:index+1], string(filepath.Separator))))
		}
		if index == len(parts)-1 {
			if wantDirectory && !info.IsDir() {
				return errors.New("must be a directory")
			}
			if !wantDirectory && !info.Mode().IsRegular() {
				return errors.New("must be a regular file")
			}
			if !wantDirectory && info.Size() > maxBytes {
				return fmt.Errorf("file exceeds the %d-byte limit", maxBytes)
			}
		}
	}
	return nil
}

func validateRelativePath(path, label string) error {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) || filepath.Clean(path) != path || path == "." || strings.HasPrefix(path, ".."+string(filepath.Separator)) || path == ".." {
		return fmt.Errorf("%s %q must be a clean repository-relative path", label, path)
	}
	if filepath.ToSlash(path) != path {
		return fmt.Errorf("%s %q must use slash separators", label, path)
	}
	return nil
}

func (m Manifest) RepositoryPath() string {
	return filepath.Join(m.baseDir, filepath.FromSlash(m.Repository.Fixture))
}

func (m Manifest) GlyphrunPath(path string) string {
	root := m.baseDir
	if m.oracleRoot != "" {
		root = m.oracleRoot
	}
	return filepath.Join(root, filepath.FromSlash(path))
}

// TreeDigest hashes relative paths, permissions, and contents while rejecting
// links, special files, oversized files, excessive aggregate data, and
// excessive path cardinality. It never executes the fixture.
func TreeDigest(root string) (string, error) {
	return treeDigestWithLimits(root, defaultFixtureLimits)
}
