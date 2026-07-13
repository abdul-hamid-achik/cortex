package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Veclite adapts the veclite CLI for cross-case disproof recall, the fourth
// memory layer. It is CLI-only (never a Go dependency) and
// best-effort: a missing binary or unreachable ollama degrades to a warning,
// never a hard failure. Resolved hypotheses (rejected/challenged are the gold)
// and definitive verification receipts are indexed into a veclite collection;
// at orient/investigate time prior related cases surface as low-confidence
// model_inference evidence so a weak model stops re-forming the same wrong theory.
type Veclite struct {
	tool
	dbPath      string
	embedModel  string
	embedURL    string
	embedClient *http.Client
	enabled     bool

	// ensureOnce guards lazy collection/space creation so the cost is paid once
	// per process; the underlying veclite ops are idempotent anyway, so a race
	// between concurrent investigate steps is safe.
	ensureOnce sync.Once
	ensureErr  error

	// embedFn is overridable in tests (defaults to the ollama HTTP call).
	embedFn func(ctx context.Context, text string) ([]float64, error)
}

// VecliteConfig threads cross-case recall config from the kernel into the adapter.
type VecliteConfig struct {
	DBPath     string
	EmbedModel string
	EmbedURL   string
	Enabled    bool
}

// NewVeclite builds a veclite adapter with sensible defaults (overridden by the
// kernel from config before any op runs).
func NewVeclite() *Veclite {
	v := &Veclite{
		tool:        newTool("veclite", 30*time.Second),
		embedModel:  "nomic-embed-text",
		embedURL:    "http://localhost:11434/api/embeddings",
		embedClient: &http.Client{Timeout: 20 * time.Second},
		enabled:     true,
	}
	v.embedFn = v.ollamaEmbed
	return v
}

func (v *Veclite) Name() string               { return "veclite" }
func (v *Veclite) Capabilities() []Capability { return []Capability{CapabilityRecall} }

// Configure applies the kernel-resolved recall config. Empty strings keep the
// defaults; Enabled=false makes IndexCase/RecallCases no-op so recall can be
// disabled without removing the binary.
func (v *Veclite) Configure(c VecliteConfig) {
	if c.DBPath != "" {
		v.dbPath = c.DBPath
	}
	if c.EmbedModel != "" {
		v.embedModel = c.EmbedModel
	}
	if c.EmbedURL != "" {
		v.embedURL = c.EmbedURL
	}
	v.enabled = c.Enabled
}

// Health probes veclite via `veclite version` (it has no doctor subcommand).
func (v *Veclite) Health(ctx context.Context) error {
	if !binExists(v.bin) {
		return ErrToolMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, _, err := v.run.run(ctx, "", v.bin, "version")
	return err
}

// Execute routes veclite operations. The investigate step uses `case_recall`.
func (v *Veclite) Execute(ctx context.Context, req Request) (Result, error) {
	if !binExists(v.bin) {
		return unavailable("veclite", req.Operation, "not on PATH"), nil
	}
	switch req.Operation {
	case "case_recall":
		return v.caseRecallOp(ctx, req.Str("query"), req.Str("repo"), req.Int("limit", 5))
	default:
		return Result{Tool: "veclite", Operation: req.Operation, Status: StatusError,
			Summary: "unknown veclite operation: " + req.Operation}, nil
	}
}

// ollamaEmbedResp is the subset of ollama's /api/embeddings response we read.
type ollamaEmbedResp struct {
	Embedding []float64 `json:"embedding"`
}

// ollamaEmbed calls the configured ollama endpoint to embed text. This is the
// ONLY network call in cortex (vecgrep does its own embedding internally); keep
// it isolated to this adapter.
func (v *Veclite) ollamaEmbed(ctx context.Context, text string) ([]float64, error) {
	body, _ := json.Marshal(map[string]string{"model": v.embedModel, "prompt": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.embedURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.embedClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embed %s: HTTP %d", v.embedModel, resp.StatusCode)
	}
	var out ollamaEmbedResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embedding) == 0 {
		return nil, fmt.Errorf("ollama returned an empty embedding for %s", v.embedModel)
	}
	return out.Embedding, nil
}

// ensureSchema creates the cortex-cases collection (with BM25 text indexing on
// the payload fields we search) and its 768-d text space, ignoring "already
// exists". Guarded by sync.Once so it runs once per process.
func (v *Veclite) ensureSchema(ctx context.Context) error {
	v.ensureOnce.Do(func() {
		// Collection — ignore "already exists" errors.
		_, _, _, err := v.execOnce(ctx, "", "create-collection", v.dbPath, "cortex-cases",
			"--text-index=statement,goal,resolved_reason", "--json")
		if err != nil && !containsFold(fmt.Sprintf("%v", err), "exists") {
			v.ensureErr = err
			return
		}
		// 768-d text space for the nomic embedding model.
		_, _, _, err = v.execOnce(ctx, "", "space-add",
			"-name", "text", "-dim", "768", "-provider", "ollama", "-model", v.embedModel,
			v.dbPath, "cortex-cases", "--json")
		if err != nil && !containsFold(fmt.Sprintf("%v", err), "exists") {
			v.ensureErr = err
		}
	})
	return v.ensureErr
}

// IndexRecord is one cross-case record indexed for recall. Embed
// text = statement + "\n" + goal + "\n" + resolved_reason. The kernel redacts
// every field and skips sensitive records BEFORE building this; the adapter
// trusts its inputs.
type IndexRecord struct {
	Key            string // "<taskID>/<hypID>" or "<taskID>/<vrID>"
	Kind           string // "hypothesis" | "verification"
	TaskID         string
	Repo           string
	Goal           string
	Statement      string
	Status         string
	Confidence     string
	DisproveBy     string
	ResolvedReason string
	Surface        string
	Artifact       string
	Timestamp      time.Time
}

// embedText is the string both embedded (vector) and BM25-indexed (content).
func (r IndexRecord) embedText() string {
	return strings.Join([]string{r.Statement, r.Goal, r.ResolvedReason}, "\n")
}

// IndexCase indexes one record. It is a WRITE — execOnce, never retried.
// Best-effort: a missing binary or failed embed returns an error
// the caller warns on; it never blocks the calling phase.
func (v *Veclite) IndexCase(ctx context.Context, rec IndexRecord) error {
	if !v.enabled || !binExists(v.bin) {
		return ErrToolMissing
	}
	if err := v.ensureSchema(ctx); err != nil {
		return err
	}
	vec, err := v.embedFn(ctx, rec.embedText())
	if err != nil {
		return err
	}
	vecJSON, _ := json.Marshal(map[string][]float64{"text": vec})
	payload := map[string]any{
		"kind":            rec.Kind,
		"task_id":         rec.TaskID,
		"repo":            rec.Repo,
		"goal":            rec.Goal,
		"statement":       rec.Statement,
		"status":          rec.Status,
		"confidence":      rec.Confidence,
		"disprove_by":     rec.DisproveBy,
		"resolved_reason": rec.ResolvedReason,
		"surface":         rec.Surface,
		"artifact":        rec.Artifact,
		"timestamp":       rec.Timestamp.UTC().Format(time.RFC3339),
	}
	payloadJSON, _ := json.Marshal(payload)
	_, _, _, err = v.execOnce(ctx, "", "record-upsert-by-key", v.dbPath, "cortex-cases",
		"--key-field=record_key", "--key-value="+rec.Key,
		"--vectors="+string(vecJSON), "--content="+rec.embedText(),
		"--payload="+string(payloadJSON), "--json")
	return err
}

// RecallHit is one recalled prior case (veclite hybrid-search-space result).
type RecallHit struct {
	ID      int64                  `json:"id"`
	Score   float64                `json:"score"`
	Payload map[string]interface{} `json:"payload"`
}

// RecallCases runs a hybrid (vector + BM25) search over the cortex-cases
// collection, scoped to repo when non-empty. An authoritative empty result
// (no prior cases) returns nil, nil — never a fabricated empty masquerading as
// success vs failure.
func (v *Veclite) RecallCases(ctx context.Context, query, repo string, limit int) ([]RecallHit, error) {
	if !v.enabled || !binExists(v.bin) {
		return nil, ErrToolMissing
	}
	if err := v.ensureSchema(ctx); err != nil {
		// A DB that doesn't exist yet means "no prior cases" — treat as empty.
		if containsFold(fmt.Sprintf("%v", err), "no such") || containsFold(fmt.Sprintf("%v", err), "not found") {
			return nil, nil
		}
		return nil, err
	}
	if limit < 1 {
		limit = 5
	}
	vec, err := v.embedFn(ctx, query)
	if err != nil {
		return nil, err
	}
	vecJSON, _ := json.Marshal(vec)
	args := []string{"hybrid-search-space", v.dbPath, "cortex-cases", "text",
		"--query=" + string(vecJSON), "--text=" + query, "--top-k=" + strconv.Itoa(limit), "--json"}
	if repo != "" {
		args = append(args, "--filter=repo="+repo)
	}
	stdout, stderr, code, err := v.exec(ctx, "", args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("veclite hybrid-search-space exit %d: %s", code, clip(stderr, 120))
	}
	var hits []RecallHit
	if derr := decodeJSON(stdout, &hits); derr != nil {
		return nil, derr
	}
	return hits, nil
}

// caseRecallOp wraps RecallCases into a Result for the investigate step. Prior
// cases are model_inference/low — orientation, never proof.
func (v *Veclite) caseRecallOp(ctx context.Context, query, repo string, limit int) (Result, error) {
	if query == "" {
		return Result{Tool: "veclite", Operation: "case_recall", Status: StatusError, Summary: "recall needs a query"}, nil
	}
	hits, err := v.RecallCases(ctx, query, repo, limit)
	if err != nil {
		return unavailable("veclite", "case_recall", err.Error()), nil
	}
	facts := make([]Fact, 0, len(hits))
	for _, h := range hits {
		facts = append(facts, Fact{Kind: "model_inference", Confidence: "low", Claim: RecallClaim(h.Payload)})
	}
	return Result{
		Tool: "veclite", Operation: "case_recall", Status: StatusAuthoritative,
		Summary: fmt.Sprintf("recalled %s for %q", pluralize(len(hits), "prior case"), clip(query, 40)),
		Facts:   facts,
		Raw:     "",
	}, nil
}

// RecallClaim renders a prior case into a model-facing orientation line.
func RecallClaim(p map[string]interface{}) string {
	str := func(k string) string {
		if s, ok := p[k].(string); ok {
			return s
		}
		return ""
	}
	kind, taskID, repo := str("kind"), str("task_id"), str("repo")
	stmt, status, reason := str("statement"), str("status"), str("resolved_reason")
	scope := ""
	if repo != "" {
		scope = " (repo " + repo + ")"
	}
	if kind == "verification" {
		return fmt.Sprintf("PRIOR CASE %s%s: verification %s — %s", taskID, scope, status, clip(str("surface"), 60))
	}
	return fmt.Sprintf("PRIOR CASE %s%s: hypothesis %q was %s — %s", taskID, scope, clip(stmt, 80), status, clip(reason, 100))
}
