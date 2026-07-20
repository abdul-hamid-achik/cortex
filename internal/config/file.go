package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"gopkg.in/yaml.v3"
)

// fileConfig is the on-disk cortex.yaml schema. Pointers distinguish "unset"
// from a zero value so a partial file only overrides what it names. YAML tags
// live here (not on domain.Budget) to keep the domain package transport-free.
type fileConfig struct {
	Budget         *budgetFile                    `yaml:"budget"`
	RedactLiterals []string                       `yaml:"redact_literals"`
	CasesDir       string                         `yaml:"cases_dir"`
	Recall         *recallFile                    `yaml:"recall"`
	Verifiers      map[string]commandVerifierFile `yaml:"verifiers"`
}

type commandVerifierFile struct {
	Argv    []string `yaml:"argv"`
	Kind    string   `yaml:"kind"`
	Surface string   `yaml:"surface"`
	Timeout string   `yaml:"timeout"`
}

type recallFile struct {
	Enabled    *bool   `yaml:"enabled"`
	DBPath     *string `yaml:"db_path"`
	EmbedModel *string `yaml:"embed_model"`
	EmbedURL   *string `yaml:"embed_url"`
}

type budgetFile struct {
	MaxParallelCalls          *int `yaml:"max_parallel_calls"`
	MaxInvestigationRounds    *int `yaml:"max_investigation_rounds"`
	MaxRawOutputBytesPerTool  *int `yaml:"max_raw_output_bytes_per_tool"`
	MaxEvidenceItemsReturned  *int `yaml:"max_evidence_items_returned"`
	MaxCandidateFilesReturned *int `yaml:"max_candidate_files_returned"`
	MaxAutoRetriesPerTool     *int `yaml:"max_auto_retries_per_tool"`
}

// Sources returns the config files that were found and applied, in increasing
// precedence order — useful for `cortex config` and debugging.
func (c Config) Sources() []string { return c.sources }

// load layers configuration onto cfg from files then environment. Precedence,
// lowest to highest: built-in defaults → global → project .config → project
// root cortex.yml/.yaml → CORTEX_* env vars.
func load(cfg *Config) {
	for _, p := range searchPaths(cfg.Workspace) {
		fc, found, err := readConfigFile(p)
		if err != nil {
			cfg.problems = append(cfg.problems, fmt.Errorf("invalid config %s: %w", p, err))
			continue
		}
		if found {
			applyFile(cfg, fc)
			cfg.sources = append(cfg.sources, p)
		}
	}
	applyEnv(cfg)
}

func searchPaths(workspace string) []string {
	return []string{
		filepath.Join(Home(), "config.yaml"),
		filepath.Join(workspace, ".config", "cortex.yaml"),
		filepath.Join(workspace, "cortex.yml"),
		filepath.Join(workspace, "cortex.yaml"),
	}
}

func readConfigFile(path string) (fileConfig, bool, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is a fixed config filename under the workspace or global config dir
	if err != nil {
		if os.IsNotExist(err) {
			return fileConfig{}, false, nil
		}
		return fileConfig{}, false, err
	}
	var fc fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fc); err == io.EOF {
		return fc, true, nil
	} else if err != nil {
		return fileConfig{}, true, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fileConfig{}, true, fmt.Errorf("multiple YAML documents are not supported")
		}
		return fileConfig{}, true, err
	}
	return fc, true, nil
}

func applyFile(cfg *Config, fc fileConfig) {
	if b := fc.Budget; b != nil {
		setInt(&cfg.Budget.MaxParallelCalls, b.MaxParallelCalls)
		setInt(&cfg.Budget.MaxInvestigationRounds, b.MaxInvestigationRounds)
		setInt(&cfg.Budget.MaxRawOutputBytesPerTool, b.MaxRawOutputBytesPerTool)
		setInt(&cfg.Budget.MaxEvidenceItemsReturned, b.MaxEvidenceItemsReturned)
		setInt(&cfg.Budget.MaxCandidateFilesReturned, b.MaxCandidateFilesReturned)
		setInt(&cfg.Budget.MaxAutoRetriesPerTool, b.MaxAutoRetriesPerTool)
	}
	if rc := fc.Recall; rc != nil {
		setBool(&cfg.Recall.Enabled, rc.Enabled)
		setStr(&cfg.Recall.DBPath, rc.DBPath)
		setStr(&cfg.Recall.EmbedModel, rc.EmbedModel)
		setStr(&cfg.Recall.EmbedURL, rc.EmbedURL)
	}
	if len(fc.RedactLiterals) > 0 {
		cfg.RedactLiterals = append(cfg.RedactLiterals, fc.RedactLiterals...)
	}
	if fc.CasesDir != "" {
		cfg.CasesDir = resolveCasesDir(cfg.Workspace, fc.CasesDir)
	}
	for name, raw := range fc.Verifiers {
		timeout := 2 * time.Minute
		if raw.Timeout != "" {
			parsed, err := time.ParseDuration(raw.Timeout)
			if err != nil {
				cfg.problems = append(cfg.problems, fmt.Errorf("command verifier %q has invalid timeout %q", name, raw.Timeout))
				continue
			}
			timeout = parsed
		}
		kind := domain.EvidenceKind(raw.Kind)
		if kind == "" {
			kind = domain.KindUnitTest
		}
		surface := domain.Surface(raw.Surface)
		if surface == "" {
			surface = domain.SurfaceCode
		}
		cfg.Verifiers[name] = CommandVerifier{
			Argv: append([]string(nil), raw.Argv...), Kind: kind,
			Surface: surface, Timeout: timeout,
		}
	}
}

// applyEnv lets CORTEX_* variables override file/default values (highest
// precedence, so CI and one-off runs can tune the kernel without editing a file).
func applyEnv(cfg *Config) {
	envInt("CORTEX_MAX_PARALLEL_CALLS", &cfg.Budget.MaxParallelCalls)
	envInt("CORTEX_MAX_INVESTIGATION_ROUNDS", &cfg.Budget.MaxInvestigationRounds)
	envInt("CORTEX_MAX_RAW_OUTPUT_BYTES", &cfg.Budget.MaxRawOutputBytesPerTool)
	envInt("CORTEX_MAX_EVIDENCE_ITEMS", &cfg.Budget.MaxEvidenceItemsReturned)
	envInt("CORTEX_MAX_CANDIDATE_FILES", &cfg.Budget.MaxCandidateFilesReturned)
	envInt("CORTEX_MAX_AUTO_RETRIES", &cfg.Budget.MaxAutoRetriesPerTool)
	if v := os.Getenv("CORTEX_REDACT_LITERALS"); v != "" {
		for _, lit := range strings.Split(v, ",") {
			if s := strings.TrimSpace(lit); s != "" {
				cfg.RedactLiterals = append(cfg.RedactLiterals, s)
			}
		}
	}
	if v := os.Getenv("CORTEX_CASES_DIR"); v != "" {
		cfg.CasesDir = resolveCasesDir(cfg.Workspace, v)
	}
	envBool("CORTEX_RECALL_ENABLED", &cfg.Recall.Enabled)
	envStr("CORTEX_RECALL_DB", &cfg.Recall.DBPath)
	envStr("CORTEX_RECALL_EMBED_MODEL", &cfg.Recall.EmbedModel)
	envStr("CORTEX_RECALL_EMBED_URL", &cfg.Recall.EmbedURL)
}

func resolveCasesDir(workspace, dir string) string {
	dir = ExpandPath(dir)
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(workspace, dir)
}

func setInt(dst *int, src *int) {
	if src != nil {
		*dst = *src
	}
}

func envInt(key string, dst *int) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

func setBool(dst *bool, src *bool) {
	if src != nil {
		*dst = *src
	}
}

func setStr(dst *string, src *string) {
	if src != nil {
		*dst = *src
	}
}

func envStr(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func envBool(key string, dst *bool) {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			*dst = true
		case "0", "false", "no", "off":
			*dst = false
		}
	}
}
