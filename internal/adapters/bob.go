package adapters

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	bobSchemaVersion       = 1
	bobContextByteLimit    = 6 << 10
	bobPathByteLimit       = 8 << 10
	bobEnvelopeByteLimit   = 64 << 10
	bobEnvelopeListLimit   = 16
	bobEnvelopeStringLimit = 512
	bobRepositoryFactKind  = "repository_contract"
)

// Bob adapts Bob's stable, read-only repository-contract CLI. It deliberately
// exposes only context and path classification: Cortex never invokes Bob's
// planner, renderer, check, or apply paths through this adapter.
type Bob struct{ tool }

// NewBob builds a Bob adapter with a short read-only repository query budget.
func NewBob() *Bob { return &Bob{tool: newTool("bob", 20*time.Second)} }

func (b *Bob) Name() string { return "bob" }

func (b *Bob) Capabilities() []Capability { return []Capability{CapabilityRepositoryContract} }

// Health uses Bob's versioned JSON command. Bob v0.4.0 intentionally does not
// expose a root --version flag, so this exact argv is part of the adapter
// contract.
func (b *Bob) Health(ctx context.Context) error {
	if !binExists(b.bin) {
		return ErrToolMissing
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stdout, _, exit, err := b.exec(probeCtx, "", "--json", "version")
	if err != nil {
		return fmt.Errorf("bob version probe: %w", err)
	}
	envelope, err := decodeBobEnvelope(stdout, "version")
	if err != nil {
		return fmt.Errorf("bob version probe: %w", err)
	}
	if envelope.OK == nil || !*envelope.OK || exit != 0 {
		return fmt.Errorf("bob version probe returned incoherent status ok=%t exit=%d", envelope.OK != nil && *envelope.OK, exit)
	}
	var version bobVersionData
	if err := decodeBobStrict(envelope.Data, &version); err != nil {
		return fmt.Errorf("bob version data: %w", err)
	}
	if version.Name != "bob" || strings.TrimSpace(version.Version) == "" {
		return errors.New("bob version data is invalid")
	}
	return nil
}

// Execute supports only the read-only context and path operations.
func (b *Bob) Execute(ctx context.Context, req Request) (Result, error) {
	switch req.Operation {
	case "context":
		return b.context(ctx, req)
	case "path":
		return b.path(ctx, req)
	default:
		return Result{
			Tool: "bob", Operation: req.Operation, Status: StatusError,
			Summary:  "unknown bob operation: " + req.Operation,
			Warnings: []string{"bob adapter permits only context and path"},
		}, nil
	}
}

func (b *Bob) context(ctx context.Context, req Request) (Result, error) {
	workspace, err := bobWorkspace(req)
	if err != nil {
		return bobInputError("context", err), nil
	}
	if !binExists(b.bin) {
		return unavailable("bob", "context", "not on PATH"), nil
	}
	stdout, stderr, exit, runErr := b.exec(ctx, workspace, "--json", "context", workspace, "--profile", "compact")
	// Check the caller context independently of the runner result: a test runner
	// or platform shim may return output concurrently with cancellation, but that
	// still must remain control flow rather than Bob evidence.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{}, ctxErr
	}
	if runErr != nil {
		// A caller cancellation is not evidence that Bob is unavailable. Preserve
		// it as control flow so the kernel can abort orientation without writing a
		// durable, contradictory tool_unavailable fact. An adapter-local timeout
		// still degrades honestly because the caller context remains live.
		return unavailable("bob", "context", runErr.Error()), nil
	}
	envelope, decodeErr := decodeBobEnvelope(stdout, "context")
	if decodeErr != nil {
		return bobInvalidOutput("context", stdout, stderr, decodeErr), nil
	}
	if envelope.OK == nil {
		return bobInvalidOutput("context", stdout, stderr, errors.New("missing ok status")), nil
	}
	if !*envelope.OK {
		return bobFailure("context", workspace, stdout, stderr, exit, envelope), nil
	}
	if exit != 0 {
		return bobInvalidOutput("context", stdout, stderr, fmt.Errorf("ok response exited %d", exit)), nil
	}
	var data bobContextData
	if err := decodeBobStrict(envelope.Data, &data); err != nil {
		return bobInvalidOutput("context", stdout, stderr, fmt.Errorf("decode data: %w", err)), nil
	}
	if err := validateBobContext(data, workspace); err != nil {
		return bobInvalidOutput("context", stdout, stderr, err), nil
	}
	attributes := map[string]string{
		"schema_version":      strconv.Itoa(data.SchemaVersion),
		"workspace":           workspace,
		"context_digest":      data.ContextDigest,
		"contract_digest":     data.ContractDigest,
		"repository_state":    data.Repository.State,
		"recipe_id":           data.Recipe.ID,
		"recipe_version":      strconv.Itoa(data.Recipe.Version),
		"plan_digest":         data.Repository.PlanDigest,
		"plan_digest_version": strconv.Itoa(data.Repository.PlanDigestVersion),
		"profile":             data.Profile,
		"bob_truncated":       strconv.FormatBool(data.Truncation.Truncated),
	}
	claim := fmt.Sprintf("Bob reports %s@%d repository state %s; contract_digest=%s; context_digest=%s; plan_digest_version=%d; plan_digest=%s", data.Recipe.ID, data.Recipe.Version, data.Repository.State, data.ContractDigest, data.ContextDigest, data.Repository.PlanDigestVersion, data.Repository.PlanDigest)
	warnings := bobWarnings(envelope.Warnings, stderr)
	if data.Repository.State != "clean" {
		warnings = append(warnings, "Bob reports repository state "+data.Repository.State)
	}
	status := StatusAuthoritative
	if data.Truncation.Truncated {
		status = StatusPartial
		warnings = append(warnings, "Bob context output was truncated within its 6144-byte contract")
	}
	return Result{
		Tool: "bob", Operation: "context", Status: status,
		Summary: claim,
		Facts: []Fact{{
			Kind: bobRepositoryFactKind, Claim: claim, Confidence: "high",
			URI:        "bob://context/v1/" + strings.TrimPrefix(data.ContextDigest, "sha256:"),
			Attributes: attributes,
		}},
		Warnings: warnings,
		Raw:      stdout,
	}, nil
}

func (b *Bob) path(ctx context.Context, req Request) (Result, error) {
	workspace, err := bobWorkspace(req)
	if err != nil {
		return bobInputError("path", err), nil
	}
	relative, err := normalizeBobPath(req.Str("path"))
	if err != nil {
		return bobInputError("path", err), nil
	}
	if !binExists(b.bin) {
		return unavailable("bob", "path", "not on PATH"), nil
	}
	stdout, stderr, exit, runErr := b.exec(ctx, workspace, "--json", "path", "--workspace", workspace, "--", relative)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{}, ctxErr
	}
	if runErr != nil {
		return unavailable("bob", "path", runErr.Error()), nil
	}
	envelope, decodeErr := decodeBobEnvelope(stdout, "path")
	if decodeErr != nil {
		return bobInvalidOutput("path", stdout, stderr, decodeErr), nil
	}
	if envelope.OK == nil {
		return bobInvalidOutput("path", stdout, stderr, errors.New("missing ok status")), nil
	}
	if !*envelope.OK {
		return bobFailure("path", workspace, stdout, stderr, exit, envelope), nil
	}
	if exit != 0 {
		return bobInvalidOutput("path", stdout, stderr, fmt.Errorf("ok response exited %d", exit)), nil
	}
	var data bobPathData
	if err := decodeBobStrict(envelope.Data, &data); err != nil {
		return bobInvalidOutput("path", stdout, stderr, fmt.Errorf("decode data: %w", err)), nil
	}
	if err := validateBobPath(data, workspace, relative); err != nil {
		return bobInvalidOutput("path", stdout, stderr, err), nil
	}
	attributes := map[string]string{
		"schema_version":    strconv.Itoa(data.SchemaVersion),
		"workspace":         workspace,
		"path":              data.Path,
		"classification":    data.Classification,
		"state":             data.State,
		"human_edit_effect": data.HumanEditEffect,
		"recipe_id":         data.Ownership.Recipe.ID,
		"recipe_version":    strconv.Itoa(data.Ownership.Recipe.Version),
		"extension_points":  bobStringArray(data.ExtensionPoints),
		"related_playbooks": bobStringArray(data.RelatedPlaybooks),
		"bob_truncated":     strconv.FormatBool(data.Truncation.Truncated),
	}
	if data.Artifact != nil {
		attributes["artifact_id"] = data.Artifact.ID
	}
	claim := fmt.Sprintf("Bob classifies %s as %s (%s); human_edit_effect=%s", data.Path, data.Classification, data.State, data.HumanEditEffect)
	if data.Artifact != nil {
		claim += "; artifact=" + data.Artifact.ID
	}
	if len(data.ExtensionPoints) > 0 {
		claim += "; extension_points=" + bobStringArray(data.ExtensionPoints)
	}
	if len(data.RelatedPlaybooks) > 0 {
		claim += "; related_playbooks=" + bobStringArray(data.RelatedPlaybooks)
	}
	canonical, _ := json.Marshal(data) // validation above already proved this value is encodable
	digest := sha256.Sum256(canonical)
	warnings := bobWarnings(envelope.Warnings, stderr)
	status := StatusAuthoritative
	if data.Truncation.Truncated {
		status = StatusPartial
		warnings = append(warnings, "Bob path output was truncated within its 8192-byte contract")
	}
	return Result{
		Tool: "bob", Operation: "path", Status: status,
		Summary: claim,
		Facts: []Fact{{
			Kind: bobRepositoryFactKind, Claim: claim, Confidence: "high",
			Location:   &Location{File: data.Path},
			URI:        "bob://path/v1/" + hex.EncodeToString(digest[:]),
			Attributes: attributes,
		}},
		Warnings: warnings,
		Raw:      stdout,
	}, nil
}

func bobWorkspace(req Request) (string, error) {
	workspace := req.Str("workspace")
	if workspace == "" {
		workspace = req.Str("dir")
	}
	if workspace == "" {
		return "", errors.New("workspace is required")
	}
	if !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace {
		return "", errors.New("workspace must be an absolute canonical path")
	}
	if strings.ContainsRune(workspace, '\x00') || !utf8.ValidString(workspace) {
		return "", errors.New("workspace must be valid UTF-8 without NUL")
	}
	return canonicalBobWorkspace(workspace), nil
}

func normalizeBobPath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') || !utf8.ValidString(value) || len(value) > 4096 {
		return "", errors.New("path must be valid UTF-8 containing 1 to 4096 bytes")
	}
	if filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return "", errors.New("path must be repository-relative")
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path must not escape the workspace")
	}
	return filepath.ToSlash(clean), nil
}

type bobCLIEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	OK            *bool           `json:"ok"`
	Command       string          `json:"command"`
	Data          json.RawMessage `json:"data"`
	Warnings      []string        `json:"warnings"`
	NextActions   []string        `json:"next_actions"`
}

type bobVersionData struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type bobRecipeRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type bobContextData struct {
	SchemaVersion   int                 `json:"schema_version"`
	Profile         string              `json:"profile"`
	Workspace       string              `json:"workspace"`
	ContractDigest  string              `json:"contract_digest"`
	ContextDigest   string              `json:"context_digest"`
	Recipe          bobRecipeRef        `json:"recipe"`
	Product         bobProduct          `json:"product"`
	Repository      bobRepository       `json:"repository"`
	Capabilities    []bobCapability     `json:"capabilities"`
	EntryPoints     []bobEntryPoint     `json:"entry_points"`
	ExtensionPoints []bobExtensionPoint `json:"extension_points"`
	Invariants      []bobInvariant      `json:"invariants"`
	Playbooks       []bobPlaybook       `json:"playbooks"`
	Artifacts       []bobArtifact       `json:"artifacts,omitempty"`
	Notices         []bobNotice         `json:"notices"`
	Actions         []bobAction         `json:"actions"`
	Truncation      bobTruncation       `json:"truncation"`
}

type bobProduct struct {
	Name       string `json:"name"`
	Module     string `json:"module,omitempty"`
	Runtime    string `json:"runtime,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

type bobRepository struct {
	State             string `json:"state"`
	Clean             bool   `json:"clean"`
	LockChanged       bool   `json:"lock_changed"`
	ConflictCount     int    `json:"conflict_count"`
	ManagedFiles      int    `json:"managed_files"`
	PlanDigestVersion int    `json:"plan_digest_version"`
	PlanDigest        string `json:"plan_digest"`
}

type bobCapability struct {
	ID              string                 `json:"id"`
	Category        string                 `json:"category,omitempty"`
	Selection       string                 `json:"selection"`
	Materialization string                 `json:"materialization"`
	Availability    string                 `json:"availability"`
	Verification    string                 `json:"verification"`
	Summary         string                 `json:"summary,omitempty"`
	Evidence        *bobCapabilityEvidence `json:"evidence,omitempty"`
	Limitations     []string               `json:"limitations,omitempty"`
}

type bobCapabilityEvidence struct {
	ManifestFields []string `json:"manifest_fields,omitempty"`
	ArtifactIDs    []string `json:"artifact_ids,omitempty"`
	Paths          []string `json:"paths,omitempty"`
	Binary         string   `json:"binary,omitempty"`
}

type bobEntryPoint struct {
	ID            string   `json:"id"`
	Path          string   `json:"path"`
	Roles         []string `json:"roles,omitempty"`
	Ownership     string   `json:"ownership"`
	CapabilityIDs []string `json:"capability_ids,omitempty"`
}

type bobExtensionPoint struct {
	ID             string   `json:"id"`
	Purpose        string   `json:"purpose,omitempty"`
	Ownership      string   `json:"ownership"`
	CreatePatterns []string `json:"create_patterns"`
	ForbiddenPaths []string `json:"forbidden_paths,omitempty"`
	CapabilityIDs  []string `json:"capability_ids,omitempty"`
	PlaybookIDs    []string `json:"playbook_ids,omitempty"`
}

type bobInvariant struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
}

type bobPlaybook struct {
	ID             string   `json:"id"`
	Title          string   `json:"title,omitempty"`
	Applicable     bool     `json:"applicable"`
	Available      bool     `json:"available"`
	BlockedBy      []string `json:"blocked_by"`
	RequiredInputs []string `json:"required_inputs"`
	ScopeClass     string   `json:"scope_class"`
	Risk           string   `json:"risk"`
}

type bobArtifact struct {
	ID            string   `json:"id"`
	Path          string   `json:"path"`
	Roles         []string `json:"roles"`
	Ownership     string   `json:"ownership"`
	CapabilityIDs []string `json:"capability_ids"`
}

type bobNotice struct {
	ID           string   `json:"id"`
	Severity     string   `json:"severity"`
	Code         string   `json:"code"`
	Message      string   `json:"message"`
	CapabilityID string   `json:"capability_id,omitempty"`
	Paths        []string `json:"paths,omitempty"`
}

type bobAction struct {
	ID                        string   `json:"id"`
	Kind                      string   `json:"kind"`
	Effect                    string   `json:"effect"`
	CWD                       string   `json:"cwd"`
	Argv                      []string `json:"argv"`
	ReasonCode                string   `json:"reason_code"`
	RequiresExplicitAuthority bool     `json:"requires_explicit_authority"`
	BlockedBy                 []string `json:"blocked_by"`
}

type bobTruncation struct {
	Profile   string         `json:"profile"`
	ByteLimit int            `json:"byte_limit"`
	Truncated bool           `json:"truncated"`
	Omitted   map[string]int `json:"omitted"`
}

type bobPathData struct {
	SchemaVersion    int              `json:"schema_version"`
	Workspace        string           `json:"workspace"`
	Path             string           `json:"path"`
	Exists           bool             `json:"exists"`
	Classification   string           `json:"classification"`
	State            string           `json:"state"`
	HumanEditEffect  string           `json:"human_edit_effect"`
	Ownership        bobOwnership     `json:"ownership"`
	PlanAction       *bobPathAction   `json:"plan_action,omitempty"`
	Artifact         *bobPathArtifact `json:"artifact,omitempty"`
	ExtensionPoints  []string         `json:"extension_points"`
	RelatedPlaybooks []string         `json:"related_playbooks"`
	Notices          []bobNotice      `json:"notices"`
	Actions          []bobAction      `json:"actions"`
	Truncation       bobTruncation    `json:"truncation"`
}

type bobOwnership struct {
	Recipe        bobRecipeRef `json:"recipe"`
	LockedSHA256  string       `json:"locked_sha256,omitempty"`
	CurrentSHA256 string       `json:"current_sha256,omitempty"`
}

type bobPathAction struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
}

type bobPathArtifact struct {
	ID            string   `json:"id"`
	Roles         []string `json:"roles"`
	CapabilityIDs []string `json:"capability_ids"`
}

type bobErrorData struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeBobEnvelope(stdout, command string) (bobCLIEnvelope, error) {
	if len(stdout) > bobEnvelopeByteLimit {
		return bobCLIEnvelope{}, fmt.Errorf("bob envelope exceeds %d-byte bound", bobEnvelopeByteLimit)
	}
	if strings.HasSuffix(stdout, "\n…(truncated)") {
		return bobCLIEnvelope{}, errors.New("output exceeded the adapter hard bound")
	}
	var envelope bobCLIEnvelope
	if err := decodeBobStrict([]byte(stdout), &envelope); err != nil {
		return bobCLIEnvelope{}, fmt.Errorf("decode envelope: %w", err)
	}
	if envelope.SchemaVersion != bobSchemaVersion {
		return bobCLIEnvelope{}, fmt.Errorf("unsupported bob envelope schema version %d", envelope.SchemaVersion)
	}
	if envelope.Command != command {
		return bobCLIEnvelope{}, fmt.Errorf("bob command is %q, want %q", envelope.Command, command)
	}
	if envelope.OK == nil || len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
		return bobCLIEnvelope{}, errors.New("bob envelope is missing required status or data")
	}
	if envelope.Warnings == nil || envelope.NextActions == nil {
		return bobCLIEnvelope{}, errors.New("bob envelope warnings and next_actions must be arrays")
	}
	if err := validateBobEnvelopeStrings("warnings", envelope.Warnings); err != nil {
		return bobCLIEnvelope{}, err
	}
	if err := validateBobEnvelopeStrings("next_actions", envelope.NextActions); err != nil {
		return bobCLIEnvelope{}, err
	}
	return envelope, nil
}

func validateBobEnvelopeStrings(name string, values []string) error {
	if len(values) > bobEnvelopeListLimit {
		return fmt.Errorf("bob envelope %s exceeds %d-item bound", name, bobEnvelopeListLimit)
	}
	for _, value := range values {
		if !utf8.ValidString(value) || len(value) > bobEnvelopeStringLimit {
			return fmt.Errorf("bob envelope %s contains a value exceeding %d-byte bound", name, bobEnvelopeStringLimit)
		}
	}
	return nil
}

func decodeBobStrict(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON documents")
		}
		return err
	}
	return nil
}

func validateBobContext(data bobContextData, workspace string) error {
	if data.SchemaVersion != bobSchemaVersion {
		return fmt.Errorf("unsupported bob context schema version %d", data.SchemaVersion)
	}
	if data.Profile != "compact" {
		return fmt.Errorf("bob context profile is %q, want compact", data.Profile)
	}
	if !sameBobWorkspace(data.Workspace, workspace) {
		return fmt.Errorf("bob context workspace is %q, want %q", data.Workspace, workspace)
	}
	if err := validateBobRecipe(data.Recipe); err != nil {
		return err
	}
	if !bobQualifiedDigest(data.ContractDigest) || !bobQualifiedDigest(data.ContextDigest) || !bobQualifiedDigest(data.Repository.PlanDigest) {
		return errors.New("bob context contains an invalid digest")
	}
	if data.Repository.PlanDigestVersion != 1 {
		return fmt.Errorf("unsupported bob plan digest version %d", data.Repository.PlanDigestVersion)
	}
	if !bobOneOf(data.Repository.State, "clean", "drifted", "conflicted") || data.Repository.ConflictCount < 0 || data.Repository.ManagedFiles < 0 {
		return errors.New("bob context contains an invalid repository state")
	}
	switch data.Repository.State {
	case "clean":
		if !data.Repository.Clean || data.Repository.LockChanged || data.Repository.ConflictCount != 0 {
			return errors.New("bob clean repository state is internally inconsistent")
		}
	case "drifted":
		if data.Repository.Clean || data.Repository.ConflictCount != 0 {
			return errors.New("bob drifted repository state is internally inconsistent")
		}
	case "conflicted":
		if data.Repository.Clean || data.Repository.ConflictCount == 0 {
			return errors.New("bob conflicted repository state is internally inconsistent")
		}
	}
	if strings.TrimSpace(data.Product.Name) == "" || data.Capabilities == nil || data.EntryPoints == nil || data.ExtensionPoints == nil || data.Invariants == nil || data.Playbooks == nil || data.Notices == nil || data.Actions == nil {
		return errors.New("bob context is missing required bounded collections")
	}
	for _, capability := range data.Capabilities {
		if capability.ID == "" || !bobOneOf(capability.Selection, "enabled", "disabled", "required", "not_applicable") ||
			!bobOneOf(capability.Materialization, "in_sync", "missing", "drifted", "conflicted", "not_applicable", "unknown") ||
			!bobOneOf(capability.Availability, "available", "unavailable", "not_checked", "not_applicable") || capability.Verification != "not_assessed" {
			return errors.New("bob context contains an invalid capability vocabulary")
		}
	}
	for _, entry := range data.EntryPoints {
		if entry.ID == "" || entry.Path == "" || !bobOneOf(entry.Ownership, "bob_whole_file", "human") {
			return errors.New("bob context contains an invalid entry point")
		}
	}
	for _, extension := range data.ExtensionPoints {
		if extension.ID == "" || extension.Ownership != "human" || extension.CreatePatterns == nil {
			return errors.New("bob context contains an invalid extension point")
		}
	}
	for _, invariant := range data.Invariants {
		if invariant.ID == "" || strings.TrimSpace(invariant.Statement) == "" {
			return errors.New("bob context contains an invalid invariant")
		}
	}
	for _, playbook := range data.Playbooks {
		if playbook.ID == "" || playbook.BlockedBy == nil || playbook.RequiredInputs == nil ||
			!bobOneOf(playbook.ScopeClass, "single_file", "small", "multi_surface", "repository_wide") ||
			!bobOneOf(playbook.Risk, "low", "medium", "high") {
			return errors.New("bob context contains an invalid playbook summary")
		}
	}
	if err := validateBobGuidance(data.Notices, data.Actions, workspace); err != nil {
		return err
	}
	if err := validateBobTruncation(data.Truncation, "compact", bobContextByteLimit); err != nil {
		return err
	}
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) > bobContextByteLimit {
		return fmt.Errorf("bob context exceeds %d-byte data bound", bobContextByteLimit)
	}
	return nil
}

func validateBobPath(data bobPathData, workspace, relative string) error {
	if data.SchemaVersion != bobSchemaVersion {
		return fmt.Errorf("unsupported bob path schema version %d", data.SchemaVersion)
	}
	if !sameBobWorkspace(data.Workspace, workspace) {
		return fmt.Errorf("bob path workspace is %q, want %q", data.Workspace, workspace)
	}
	if data.Path != relative {
		return fmt.Errorf("bob path is %q, want %q", data.Path, relative)
	}
	if _, err := normalizeBobPath(data.Path); err != nil {
		return fmt.Errorf("bob returned unsafe path: %w", err)
	}
	if !bobOneOf(data.Classification, "managed", "reserved", "extension_point", "unmanaged", "missing") ||
		!bobOneOf(data.State, "managed_in_sync", "managed_modified", "managed_missing", "retired_owned", "extension_point", "unmanaged_present", "unmanaged_missing", "reserved", "symlink", "special_file") ||
		!bobOneOf(data.HumanEditEffect, "will_conflict", "outside_bob_ownership", "reserved_for_bob", "requires_manifest_change", "unsafe") {
		return errors.New("bob path contains an invalid classification vocabulary")
	}
	if data.ExtensionPoints == nil || data.RelatedPlaybooks == nil || data.Notices == nil || data.Actions == nil {
		return errors.New("bob path is missing required bounded collections")
	}
	if err := validateBobRecipe(data.Ownership.Recipe); err != nil {
		return err
	}
	if (data.Ownership.LockedSHA256 != "" && !bobPlainDigest(data.Ownership.LockedSHA256)) || (data.Ownership.CurrentSHA256 != "" && !bobPlainDigest(data.Ownership.CurrentSHA256)) {
		return errors.New("bob path contains an invalid ownership digest")
	}
	if data.Classification == "extension_point" && (data.State != "extension_point" || data.HumanEditEffect != "outside_bob_ownership" || len(data.ExtensionPoints) == 0) {
		return errors.New("bob extension-point classification is internally inconsistent")
	}
	if err := validateBobPathSemantics(data.Classification, data.State, data.HumanEditEffect); err != nil {
		return err
	}
	if data.PlanAction != nil && (!bobOneOf(data.PlanAction.Kind, "create", "update", "unchanged", "adopt", "conflict") ||
		!bobOneOf(data.PlanAction.Code, "unmanaged_differs", "managed_hash_mismatch", "managed_missing", "unmanaged_mode_differs", "retired_owned", "symlink", "special_file", "missing", "mode_drift", "content_update", "in_sync", "identical_content")) {
		return errors.New("bob path contains an invalid plan action vocabulary")
	}
	if data.Artifact != nil && (data.Artifact.ID == "" || data.Artifact.Roles == nil || data.Artifact.CapabilityIDs == nil) {
		return errors.New("bob path contains an invalid artifact")
	}
	if err := validateBobGuidance(data.Notices, data.Actions, workspace); err != nil {
		return err
	}
	if err := validateBobTruncation(data.Truncation, "path", bobPathByteLimit); err != nil {
		return err
	}
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) > bobPathByteLimit {
		return fmt.Errorf("bob path exceeds %d-byte data bound", bobPathByteLimit)
	}
	return nil
}

func validateBobPathSemantics(classification, state, effect string) error {
	valid := false
	switch classification {
	case "managed":
		switch state {
		case "managed_in_sync", "managed_modified", "managed_missing", "symlink", "special_file":
			valid = effect == "will_conflict"
		case "retired_owned":
			valid = effect == "reserved_for_bob"
		}
	case "reserved":
		valid = state == "reserved" && bobOneOf(effect, "reserved_for_bob", "requires_manifest_change", "unsafe")
	case "extension_point":
		valid = state == "extension_point" && effect == "outside_bob_ownership"
	case "unmanaged":
		switch state {
		case "unmanaged_present":
			valid = effect == "outside_bob_ownership"
		case "symlink", "special_file":
			valid = effect == "unsafe"
		}
	case "missing":
		valid = state == "unmanaged_missing" && effect == "outside_bob_ownership"
	}
	if !valid {
		return fmt.Errorf("bob path tuple %s/%s/%s is internally inconsistent", classification, state, effect)
	}
	return nil
}

func validateBobRecipe(recipe bobRecipeRef) error {
	if strings.TrimSpace(recipe.ID) == "" || recipe.Version <= 0 {
		return errors.New("bob result contains an invalid recipe identity")
	}
	return nil
}

func validateBobGuidance(notices []bobNotice, actions []bobAction, workspace string) error {
	for _, notice := range notices {
		if notice.ID == "" || notice.Code == "" || notice.Message == "" || !bobOneOf(notice.Severity, "info", "warning", "error") {
			return errors.New("bob result contains an invalid notice")
		}
	}
	for _, action := range actions {
		if action.ID == "" || action.Kind != "command" || action.Effect != "read_only" || !sameBobWorkspace(action.CWD, workspace) || len(action.Argv) == 0 || action.ReasonCode == "" || action.RequiresExplicitAuthority || action.BlockedBy == nil {
			return errors.New("bob result contains an invalid read-only action")
		}
	}
	return nil
}

func validateBobTruncation(truncation bobTruncation, profile string, limit int) error {
	if truncation.Profile != profile || truncation.ByteLimit != limit || truncation.Omitted == nil {
		return errors.New("bob result contains an invalid truncation contract")
	}
	for _, count := range truncation.Omitted {
		if count < 0 {
			return errors.New("bob result contains a negative omitted count")
		}
	}
	return nil
}

func bobFailure(operation, workspace, stdout, stderr string, exit int, envelope bobCLIEnvelope) Result {
	if exit == 0 {
		return bobInvalidOutput(operation, stdout, stderr, errors.New("error response exited zero"))
	}
	var data bobErrorData
	if err := decodeBobStrict(envelope.Data, &data); err != nil {
		return bobInvalidOutput(operation, stdout, stderr, fmt.Errorf("decode error data: %w", err))
	}
	if strings.TrimSpace(data.Error.Code) == "" || strings.TrimSpace(data.Error.Message) == "" {
		return bobInvalidOutput(operation, stdout, stderr, errors.New("error response lacks code or message"))
	}
	if !bobOneOf(data.Error.Code, "missing_manifest", "manifest_invalid", "input_invalid", "conflicts", "workspace_invalid", "plan_digest_mismatch", "command_failed") {
		return bobInvalidOutput(operation, stdout, stderr, fmt.Errorf("unknown bob error code %q", data.Error.Code))
	}
	claim := "Bob could not provide " + operation + ": " + data.Error.Code + " (" + clip(data.Error.Message, 180) + ")"
	warnings := bobWarnings(envelope.Warnings, stderr)
	warnings = append(warnings, claim)
	return Result{
		Tool: "bob", Operation: operation, Status: StatusPartial, Summary: claim,
		Facts: []Fact{{
			Kind: bobRepositoryFactKind, Claim: claim, Confidence: "unknown",
			Attributes: map[string]string{
				"error_code": data.Error.Code, "error_message": clip(data.Error.Message, 512),
				"command": operation, "workspace": workspace,
			},
		}},
		Warnings: warnings,
		Raw:      stdout,
	}
}

func bobInputError(operation string, err error) Result {
	return Result{
		Tool: "bob", Operation: operation, Status: StatusError,
		Summary:  "invalid bob " + operation + " input: " + err.Error(),
		Warnings: []string{"bob " + operation + ": " + err.Error()},
	}
}

func bobInvalidOutput(operation, stdout, stderr string, err error) Result {
	warnings := bobWarnings(nil, stderr)
	warnings = append(warnings, "bob "+operation+" output rejected: "+clip(err.Error(), 180))
	return Result{
		Tool: "bob", Operation: operation, Status: StatusError,
		Summary:  "invalid bob " + operation + " output: " + clip(err.Error(), 180),
		Warnings: warnings,
		Raw:      stdout,
	}
}

func bobWarnings(envelopeWarnings []string, stderr string) []string {
	warnings := append([]string(nil), envelopeWarnings...)
	if line := firstLine(stderr); line != "" {
		warnings = append(warnings, "bob stderr: "+clip(line, 180))
	}
	return warnings
}

func bobQualifiedDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && bobPlainDigest(strings.TrimPrefix(value, "sha256:"))
}

func bobPlainDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func bobOneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func bobStringArray(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func canonicalBobWorkspace(value string) string {
	clean := filepath.Clean(value)
	if evaluated, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(evaluated)
	}
	return clean
}

func sameBobWorkspace(left, right string) bool {
	if !filepath.IsAbs(left) || !filepath.IsAbs(right) {
		return false
	}
	return canonicalBobWorkspace(left) == canonicalBobWorkspace(right)
}
