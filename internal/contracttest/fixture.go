// Package contracttest supports Cortex's public, deterministic conformance
// fixtures. It is internal because consumers inspect the JSON corpus directly;
// Cortex does not publish a second runtime SDK for its own contract.
package contracttest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

const (
	// Version is the public conformance-fixture schema version.
	Version = 1
	// UpdateEnv opts a local maintainer into rewriting goldens.
	UpdateEnv = "CORTEX_UPDATE_CONTRACTS"
)

const sensitiveDataPolicy = "synthetic data only; secrets, private paths, timestamps, and nondeterministic identifiers are forbidden"

var (
	idPattern        = regexp.MustCompile(`\b(task|ev|hyp|vr|vb|dec|raw)_[0-9A-Z]+\b`)
	timestampPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z`)
	hexPattern       = regexp.MustCompile(`\b[0-9a-f]{7,64}\b`)
	tempPathPattern  = regexp.MustCompile(`(?:/private)?/(?:var/folders|tmp)/[^"\s]+`)
	// Normalize the JSON-escaped representation of Windows user/UNC paths.
	// JSON doubles each backslash, so these patterns intentionally match the
	// encoded form after marshalJSON rather than an operating-system path.
	windowsUserPathPattern = regexp.MustCompile(`(?i)\b[A-Z]:\\\\(?:Users|Documents and Settings)\\\\[^"\\\s]+(?:\\\\[^"\s]*)?`)
	windowsTempPathPattern = regexp.MustCompile(`(?i)\b[A-Z]:\\\\(?:Windows\\\\)?Temp(?:\\\\[^"\s]*)?`)
	windowsEnvTempPattern  = regexp.MustCompile(`(?i)%(?:TEMP|TMP)%(?:\\\\[^"\s]*)?`)
	uncPathPattern         = regexp.MustCompile(`\\\\\\\\[^"\\\s]+\\\\[^"\\\s]+(?:\\\\[^"\s]*)?`)
)

// Fixture is the stable wrapper shared by every public contract example.
type Fixture struct {
	ContractVersion     int             `json:"contractVersion"`
	ID                  string          `json:"id"`
	GeneratedBy         string          `json:"generatedBy"`
	Classification      string          `json:"classification"`
	SensitiveDataPolicy string          `json:"sensitiveDataPolicy"`
	SizeBehavior        string          `json:"sizeBehavior"`
	Payload             json.RawMessage `json:"payload"`
}

// NewFixture normalizes one value and wraps it with the public metadata.
func NewFixture(id, generatedBy, classification, sizeBehavior string, payload any, replacements map[string]string) (Fixture, error) {
	normalized, err := Normalize(payload, replacements)
	if err != nil {
		return Fixture{}, err
	}
	f := Fixture{
		ContractVersion: Version, ID: id, GeneratedBy: generatedBy,
		Classification: classification, SensitiveDataPolicy: sensitiveDataPolicy,
		SizeBehavior: sizeBehavior, Payload: normalized,
	}
	if err := f.Validate(); err != nil {
		return Fixture{}, err
	}
	return f, nil
}

// Validate rejects malformed and unknown future fixture schemas.
func (f Fixture) Validate() error {
	if f.ContractVersion != Version {
		return fmt.Errorf("unsupported contract fixture version %d; supported version is %d", f.ContractVersion, Version)
	}
	if strings.TrimSpace(f.ID) == "" || strings.TrimSpace(f.GeneratedBy) == "" {
		return errors.New("contract fixture id and generator are required")
	}
	if f.Classification != "canonical" && f.Classification != "illustrative" {
		return errors.New("contract fixture classification must be canonical or illustrative")
	}
	if f.SensitiveDataPolicy != sensitiveDataPolicy {
		return errors.New("contract fixture sensitive data policy is missing or unknown")
	}
	if strings.TrimSpace(f.SizeBehavior) == "" || len(f.Payload) == 0 || !json.Valid(f.Payload) {
		return errors.New("contract fixture size behavior and valid JSON payload are required")
	}
	return nil
}

// Decode validates a consumer-facing fixture and fails closed on future
// versions instead of guessing at their meaning.
func Decode(data []byte) (Fixture, error) {
	var version struct {
		ContractVersion int `json:"contractVersion"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return Fixture{}, fmt.Errorf("decode contract fixture version: %w", err)
	}
	if version.ContractVersion != Version {
		return Fixture{}, fmt.Errorf("unsupported contract fixture version %d; supported version is %d", version.ContractVersion, Version)
	}
	var fixture Fixture
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		return Fixture{}, fmt.Errorf("decode contract fixture: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Fixture{}, errors.New("decode contract fixture: multiple JSON values")
		}
		return Fixture{}, fmt.Errorf("decode contract fixture: %w", err)
	}
	if err := fixture.Validate(); err != nil {
		return Fixture{}, err
	}
	return fixture, nil
}

// Normalize makes contract payloads stable without changing their shape.
func Normalize(payload any, replacements map[string]string) (json.RawMessage, error) {
	encoded, err := marshalJSON(payload, "")
	if err != nil {
		return nil, fmt.Errorf("marshal contract payload: %w", err)
	}
	text := string(encoded)
	keys := make([]string, 0, len(replacements))
	for from := range replacements {
		if from != "" {
			keys = append(keys, from)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, from := range keys {
		text = strings.ReplaceAll(text, from, replacements[from])
	}
	text = replaceStable(text, idPattern, func(prefix string, n int) string {
		labels := map[string]string{
			"task": "TASK", "ev": "EVIDENCE", "hyp": "HYPOTHESIS",
			"vr": "RECEIPT", "vb": "BATCH", "dec": "DECISION", "raw": "RAW",
		}
		return fmt.Sprintf("$%s_ID_%d", labels[prefix], n)
	})
	text = timestampPattern.ReplaceAllStringFunc(text, func(string) string { return "$TIMESTAMP" })
	text = tempPathPattern.ReplaceAllStringFunc(text, func(string) string { return "$TEMP_PATH" })
	text = windowsUserPathPattern.ReplaceAllStringFunc(text, func(string) string { return "$WINDOWS_PRIVATE_PATH" })
	text = windowsTempPathPattern.ReplaceAllStringFunc(text, func(string) string { return "$WINDOWS_TEMP_PATH" })
	text = windowsEnvTempPattern.ReplaceAllStringFunc(text, func(string) string { return "$WINDOWS_TEMP_PATH" })
	text = uncPathPattern.ReplaceAllStringFunc(text, func(string) string { return "$UNC_PRIVATE_PATH" })
	text = replaceStable(text, hexPattern, func(_ string, n int) string {
		return fmt.Sprintf("$REVISION_OR_DIGEST_%d", n)
	})
	if !json.Valid([]byte(text)) {
		return nil, errors.New("normalized contract payload is not valid JSON")
	}
	return json.RawMessage(text), nil
}

func replaceStable(text string, pattern *regexp.Regexp, label func(string, int) string) string {
	seen := map[string]string{}
	counts := map[string]int{}
	return pattern.ReplaceAllStringFunc(text, func(match string) string {
		if replacement, ok := seen[match]; ok {
			return replacement
		}
		parts := strings.SplitN(match, "_", 2)
		prefix := ""
		if len(parts) == 2 {
			prefix = parts[0]
		}
		counts[prefix]++
		replacement := label(prefix, counts[prefix])
		seen[match] = replacement
		return replacement
	})
}

// Marshal returns the canonical indented representation used by goldens.
func Marshal(f Fixture) ([]byte, error) {
	encoded, err := marshalJSON(f, "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func marshalJSON(value any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if indent != "" {
		encoder.SetIndent("", indent)
	}
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

// Path returns a repository-relative public fixture path independent of the
// package test's working directory.
func Path(id string) string {
	_, source, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "contracts", "v1", "fixtures", id+".json"))
}

// AssertGolden compares a generated fixture with its public file. Setting
// CORTEX_UPDATE_CONTRACTS=1 rewrites it deliberately.
func AssertGolden(id string, fixture Fixture) error {
	want, err := Marshal(fixture)
	if err != nil {
		return err
	}
	path := Path(id)
	if os.Getenv(UpdateEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, want, 0o644)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read contract fixture %s: %w", id, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("contract fixture %s drifted; inspect the contract change and rerun with %s=1", id, UpdateEnv)
	}
	if _, err := Decode(got); err != nil {
		return fmt.Errorf("validate contract fixture %s: %w", id, err)
	}
	return nil
}
