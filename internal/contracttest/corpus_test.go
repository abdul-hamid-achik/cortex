package contracttest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

type manifest struct {
	ContractVersion     int                 `json:"contractVersion"`
	Classification      string              `json:"classification"`
	SensitiveDataPolicy string              `json:"sensitiveDataPolicy"`
	Groups              map[string][]string `json:"groups"`
}

func TestPublicCorpusIsCompleteValidAndNormalized(t *testing.T) {
	manifestPath := filepath.Join(filepath.Dir(Path("unused")), "..", "manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := decodeManifest(manifestData)
	if err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if catalog.ContractVersion != Version || catalog.Classification != "public conformance corpus" || catalog.SensitiveDataPolicy != sensitiveDataPolicy {
		t.Fatalf("invalid manifest metadata: %+v", catalog)
	}
	wantGroups := map[string]int{
		"coreLifecycleSuccess": 8, "structuralRejections": 8,
		"degradedAndEdge": 8, "mcpTransport": 3,
	}
	if len(catalog.Groups) != len(wantGroups) {
		t.Fatalf("manifest group count=%d, want %d", len(catalog.Groups), len(wantGroups))
	}
	wantIDs := map[string]bool{}
	for group, count := range wantGroups {
		ids := catalog.Groups[group]
		if len(ids) != count {
			t.Fatalf("manifest group %s has %d fixtures, want %d", group, len(ids), count)
		}
		for _, id := range ids {
			if wantIDs[id] {
				t.Fatalf("fixture %s appears in more than one group", id)
			}
			wantIDs[id] = true
		}
	}
	entries, err := os.ReadDir(filepath.Dir(Path("unused")))
	if err != nil {
		t.Fatal(err)
	}
	var gotIDs []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		gotIDs = append(gotIDs, id)
		data, err := os.ReadFile(Path(id))
		if err != nil {
			t.Fatal(err)
		}
		fixture, err := Decode(data)
		if err != nil {
			t.Fatalf("decode %s: %v", id, err)
		}
		if fixture.ID != id || !wantIDs[id] {
			t.Fatalf("uncatalogued or mismatched fixture file %s contains id %q", entry.Name(), fixture.ID)
		}
		canonical, err := Marshal(fixture)
		if err != nil || string(canonical) != string(data) {
			t.Fatalf("fixture %s is not canonical JSON: %v", id, err)
		}
		assertNormalizedFixture(t, id, data)
	}
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("fixture file count=%d, manifest count=%d", len(gotIDs), len(wantIDs))
	}
	sort.Strings(gotIDs)
	for _, id := range gotIDs {
		if !wantIDs[id] {
			t.Fatalf("fixture %s is not in the manifest", id)
		}
	}
}

func TestPublicFixturesValidateAgainstPublishedJSONSchema(t *testing.T) {
	schemaPath := filepath.Join(filepath.Dir(Path("unused")), "..", "schema.json")
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var published jsonschema.Schema
	if err := json.Unmarshal(schemaData, &published); err != nil {
		t.Fatalf("decode public JSON Schema: %v", err)
	}
	resolved, err := published.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve public JSON Schema: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(Path("unused")))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(filepath.Dir(Path("unused")), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var instance any
		if err := json.Unmarshal(data, &instance); err != nil {
			t.Fatalf("decode fixture %s: %v", entry.Name(), err)
		}
		if err := resolved.Validate(instance); err != nil {
			t.Fatalf("fixture %s violates published JSON Schema: %v", entry.Name(), err)
		}
	}

	invalid := map[string]any{
		"contractVersion":     Version,
		"id":                  "invalid",
		"generatedBy":         "test",
		"classification":      "unreviewed",
		"sensitiveDataPolicy": sensitiveDataPolicy,
		"sizeBehavior":        "bounded",
		"payload":             map[string]any{"ok": true},
	}
	if err := resolved.Validate(invalid); err == nil {
		t.Fatal("published JSON Schema accepted an unknown classification")
	}
	invalid["classification"] = "illustrative"
	invalid["future"] = true
	if err := resolved.Validate(invalid); err == nil {
		t.Fatal("published JSON Schema accepted an unknown wrapper property")
	}
}

func TestManifestDecoderRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	valid := `{"contractVersion":1,"classification":"public conformance corpus","sensitiveDataPolicy":"` + sensitiveDataPolicy + `","groups":{}}`
	if _, err := decodeManifest([]byte(valid)); err != nil {
		t.Fatalf("decode valid manifest: %v", err)
	}
	for _, data := range []string{
		strings.TrimSuffix(valid, "}") + `,"future":true}`,
		valid + `{}`,
	} {
		if _, err := decodeManifest([]byte(data)); err == nil {
			t.Fatalf("manifest decoder accepted %s", data)
		}
	}
}

func decodeManifest(data []byte) (manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value manifest
	if err := decoder.Decode(&value); err != nil {
		return manifest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return manifest{}, errors.New("manifest contains multiple JSON values")
		}
		return manifest{}, err
	}
	return value, nil
}

func TestPublicFixtureSchemaMatchesTheStrictWrapper(t *testing.T) {
	schemaPath := filepath.Join(filepath.Dir(Path("unused")), "..", "schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode public schema: %v", err)
	}
	additional, ok := schema["additionalProperties"].(bool)
	if !ok || additional {
		t.Fatalf("public schema must reject unknown wrapper properties: %v", schema["additionalProperties"])
	}
	requiredValues, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("public schema required is not an array: %T", schema["required"])
	}
	var required []string
	for _, value := range requiredValues {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("public schema required entry is not a string: %T", value)
		}
		required = append(required, text)
	}
	sort.Strings(required)
	wantRequired := []string{
		"classification", "contractVersion", "generatedBy", "id", "payload", "sensitiveDataPolicy", "sizeBehavior",
	}
	if strings.Join(required, "\n") != strings.Join(wantRequired, "\n") {
		t.Fatalf("public schema required=%v want=%v", required, wantRequired)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) != len(wantRequired) {
		t.Fatalf("public schema properties=%T/%d", schema["properties"], len(properties))
	}
	version, ok := properties["contractVersion"].(map[string]any)
	if !ok || version["const"] != float64(Version) {
		t.Fatalf("public schema contractVersion=%v", properties["contractVersion"])
	}
	policy, ok := properties["sensitiveDataPolicy"].(map[string]any)
	if !ok || policy["const"] != sensitiveDataPolicy {
		t.Fatalf("public schema sensitiveDataPolicy=%v", properties["sensitiveDataPolicy"])
	}
	for _, name := range []string{"id", "generatedBy", "sizeBehavior"} {
		property, ok := properties[name].(map[string]any)
		if !ok || property["pattern"] != `\S` {
			t.Fatalf("public schema %s must reject whitespace-only values: %v", name, properties[name])
		}
	}
}

func assertNormalizedFixture(t *testing.T, id string, data []byte) {
	t.Helper()
	text := string(data)
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`(?:/private)?/(?:var/folders|tmp)/`),
		regexp.MustCompile(`(?i)\b[A-Z]:\\\\(?:Users|Documents and Settings)\\\\`),
		regexp.MustCompile(`(?i)\b[A-Z]:\\\\(?:Windows\\\\)?Temp(?:\\\\|["'])`),
		regexp.MustCompile(`(?i)%(?:TEMP|TMP)%(?:\\\\|["'])`),
		regexp.MustCompile(`\\\\\\\\[^"\\\s]+\\\\[^"\\\s]+`),
		regexp.MustCompile(`\b(?:task|ev|hyp|vr|vb|dec|raw)_[0-9A-Z]+\b`),
		regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`),
		regexp.MustCompile(`\bghp_[0-9A-Za-z]+\b`),
		regexp.MustCompile(`\bsk_live_[0-9A-Za-z]+\b`),
		regexp.MustCompile(`/(?:Users|home)/[^/"\s]+/`),
	}
	if redact.New().Detected(text) {
		t.Fatalf("fixture %s contains a secret shape recognized by the repository redactor", id)
	}
	for _, pattern := range forbidden {
		if pattern.MatchString(text) {
			t.Fatalf("fixture %s contains forbidden machine-specific or sensitive value matching %s", id, pattern)
		}
	}
	if len(data) > 128<<10 {
		t.Fatalf("fixture %s is %d bytes; public examples must remain inspectable and bounded", id, len(data))
	}
}
