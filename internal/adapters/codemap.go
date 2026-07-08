package adapters

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Codemap adapts the codemap CLI for structural code evidence (SPEC §11.3,
// §12.2): impact/blast-radius, callers, symbol lookup, diff review. codemap
// uses a boolean `--json` flag and `--top`/`--depth` for limits.
type Codemap struct{ tool }

// NewCodemap builds a codemap adapter. Timeout is the SPEC §17.2
// structural_query budget (20s).
func NewCodemap() *Codemap { return &Codemap{tool: newTool("codemap", 20*time.Second)} }

func (c *Codemap) Name() string { return "codemap" }

func (c *Codemap) Capabilities() []Capability { return []Capability{CapabilityStructure} }

// Health probes codemap via `codemap --version` (codemap has a doctor
// subcommand but --version is the cheapest binary-present check).
func (c *Codemap) Health(ctx context.Context) error {
	if !binExists(c.bin) {
		return ErrToolMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, _, err := c.run.run(ctx, "", c.bin, "--version")
	return err
}

// Execute routes codemap operations. Each shells out with --json and maps the
// documented output shape into normalized facts.
func (c *Codemap) Execute(ctx context.Context, req Request) (Result, error) {
	if !binExists(c.bin) {
		return unavailable("codemap", req.Operation, "not on PATH"), nil
	}
	dir := req.Str("dir")
	switch req.Operation {
	case "impact":
		return c.impact(ctx, dir, req.Str("symbol"), req.Int("depth", 3))
	case "callers", "callees":
		return c.relation(ctx, dir, req.Operation, req.Str("symbol"))
	case "find":
		return c.find(ctx, dir, req.Str("query"), req.Int("top", 8))
	case "semantic":
		return c.semantic(ctx, dir, req.Str("query"), req.Int("top", 10))
	case "review":
		return c.review(ctx, dir, req.Str("since"), boolOf(req.Input["staged"]))
	default:
		return Result{Tool: "codemap", Operation: req.Operation, Status: StatusError,
			Summary: "unknown codemap operation: " + req.Operation}, nil
	}
}

// Annotate attaches a behavioral note to a code symbol via `codemap annotate`
// (SPEC §12.2 structural memory: link a symbol to the behavior proven about
// it). It is a write, so it uses execOnce (no retry). The source label lets
// codemap group ecosystem annotations (e.g. "cairntrace", "glyphrun").
func (c *Codemap) Annotate(ctx context.Context, dir, symbol, source, note string) error {
	if !binExists(c.bin) {
		return ErrToolMissing
	}
	if symbol == "" || note == "" {
		return fmt.Errorf("annotate needs a symbol and a note")
	}
	src := source
	if src == "" {
		src = "cortex"
	}
	_, _, code, err := c.execOnce(ctx, dir, "annotate", symbol, "--source", src, "--note", note, "--json")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("codemap annotate exited %d", code)
	}
	return nil
}

// cmEnvelope is codemap ≥0.36's structured error contract: on any --json
// failure it prints {ok:false,error,code,hint} to stdout (exit 3/4/5). OK is a
// pointer so an absent `ok` key (every SUCCESS result has none) is
// distinguishable from a genuine ok:false.
type cmEnvelope struct {
	OK    *bool  `json:"ok"`
	Error string `json:"error"`
	Code  string `json:"code"`
	Hint  string `json:"hint"`
}

// codemapError detects codemap's error envelope on stdout and maps it to an
// honest Result. It returns (result, true) only when an {ok:false} envelope is
// present, so every success path and every OLD-codemap error (cobra text on
// stderr, non-JSON stdout) falls through untouched to the op-specific decode /
// degraded path. An index/repo problem is unavailable (blocked); an operational
// error is a genuine error — never a confidently-wrong "no such symbol".
func codemapError(op, stdout string) (Result, bool) {
	var e cmEnvelope
	if decodeJSON(stdout, &e) != nil || e.OK == nil || *e.OK {
		return Result{}, false
	}
	reason := e.Error
	if e.Code != "" {
		reason = e.Code + ": " + reason
	}
	if e.Hint != "" {
		reason += " — " + e.Hint
	}
	if e.Code == "operational" {
		return Result{Tool: "codemap", Operation: op, Status: StatusError,
			Summary:  "codemap " + op + " failed: " + reason,
			Warnings: []string{"codemap: " + clip(reason, 160)},
			Raw:      stdout}, true
	}
	res := unavailable("codemap", op, reason)
	res.Operation = op
	res.Raw = stdout
	return res, true
}

// --- impact ---

type cmImpact struct {
	Symbol        string        `json:"symbol"`
	Found         bool          `json:"found"`
	Locations     []cmSymbolRef `json:"locations"`
	DirectCallers []cmSymbolRef `json:"direct_callers"`
	BlastRadius   []cmSymbolRef `json:"blast_radius"`
	Tests         []cmSymbolRef `json:"tests"`
	Untested      bool          `json:"untested"`
	Resolution    string        `json:"resolution"`
	CallGraph     string        `json:"call_graph"` // codemap ≥0.36: resolved|name|unresolved|none
	Note          string        `json:"note"`
}

type cmSymbolRef struct {
	Symbol    string `json:"symbol"`
	FQN       string `json:"fqn"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Signature string `json:"signature"`
	Doc       string `json:"doc"`
}

func (c *Codemap) impact(ctx context.Context, dir, symbol string, depth int) (Result, error) {
	if symbol == "" {
		return Result{Tool: "codemap", Operation: "impact", Status: StatusError, Summary: "impact needs a symbol"}, nil
	}
	stdout, stderr, code, err := c.exec(ctx, dir, "impact", symbol, "--depth", strconv.Itoa(depth), "--json")
	if err != nil {
		return unavailable("codemap", "impact", err.Error()), nil
	}
	if res, ok := codemapError("impact", stdout); ok {
		return res, nil
	}
	var r cmImpact
	if derr := decodeJSON(stdout, &r); derr != nil {
		return degraded("codemap", "impact", stdout, stderr, code), nil
	}
	if !r.Found {
		return Result{Tool: "codemap", Operation: "impact", Status: StatusPartial,
			Summary: "codemap found no symbol " + symbol, Raw: stdout}, nil
	}
	conf := callGraphConfidence(r.CallGraph, r.Resolution)
	facts := []Fact{{
		Kind:       "code_graph",
		Claim:      fmt.Sprintf("%s has %s in its blast radius and %s covering it", symbol, pluralize(len(r.BlastRadius), "symbol"), pluralize(len(r.Tests), "test")),
		Confidence: conf,
	}}
	for _, loc := range r.Locations {
		facts = append(facts, Fact{Kind: "code_location", Confidence: conf,
			Claim:    "defined at " + loc.File,
			Location: &Location{File: loc.File, StartLine: loc.StartLine, EndLine: loc.EndLine, Symbol: symbol}})
	}
	warns := noteWarnings(r.Note, r.Untested, symbol)
	warns = appendUnresolvedHint(warns, r.CallGraph, r.Resolution)
	return Result{
		Tool: "codemap", Operation: "impact", Status: StatusAuthoritative,
		Summary:  facts[0].Claim,
		Facts:    facts,
		Warnings: warns,
		Raw:      stdout,
	}, nil
}

// --- callers / callees ---

type cmRelation struct {
	Symbol     string        `json:"symbol"`
	Found      bool          `json:"found"`
	Results    []cmSymbolRef `json:"results"`
	Note       string        `json:"note"`
	Resolution string        `json:"resolution"`
	CallGraph  string        `json:"call_graph"`
}

func (c *Codemap) relation(ctx context.Context, dir, op, symbol string) (Result, error) {
	if symbol == "" {
		return Result{Tool: "codemap", Operation: op, Status: StatusError, Summary: op + " needs a symbol"}, nil
	}
	stdout, stderr, code, err := c.exec(ctx, dir, op, symbol, "--json")
	if err != nil {
		return unavailable("codemap", op, err.Error()), nil
	}
	if res, ok := codemapError(op, stdout); ok {
		return res, nil
	}
	var r cmRelation
	if derr := decodeJSON(stdout, &r); derr != nil {
		return degraded("codemap", op, stdout, stderr, code), nil
	}
	conf := callGraphConfidence(r.CallGraph, r.Resolution)
	return Result{
		Tool: "codemap", Operation: op, Status: StatusAuthoritative,
		Summary: fmt.Sprintf("%s: %s", op, pluralize(len(r.Results), "result")),
		Facts: []Fact{{Kind: "code_graph", Confidence: conf,
			Claim: fmt.Sprintf("%s has %s (%s)", symbol, pluralize(len(r.Results), op[:len(op)-1]), op)}},
		Warnings: appendUnresolvedHint(noteWarnings(r.Note, false, symbol), r.CallGraph, r.Resolution),
		Raw:      stdout,
	}, nil
}

// --- find / semantic ---

type cmSemantic struct {
	Query string     `json:"query"`
	Mode  string     `json:"mode"`
	Note  string     `json:"note"`
	Hits  []cmSemHit `json:"hits"`
}

type cmSemHit struct {
	Symbol    string  `json:"symbol"`
	FQN       string  `json:"fqn"`
	Kind      string  `json:"kind"`
	File      string  `json:"file"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
	Signature string  `json:"signature"`
}

func (c *Codemap) find(ctx context.Context, dir, query string, top int) (Result, error) {
	return c.searchLike(ctx, dir, "find", query, top)
}

func (c *Codemap) semantic(ctx context.Context, dir, query string, top int) (Result, error) {
	return c.searchLike(ctx, dir, "semantic", query, top)
}

func (c *Codemap) searchLike(ctx context.Context, dir, op, query string, top int) (Result, error) {
	if query == "" {
		return Result{Tool: "codemap", Operation: op, Status: StatusError, Summary: op + " needs a query"}, nil
	}
	stdout, stderr, code, err := c.exec(ctx, dir, op, query, "--top", strconv.Itoa(top), "--json")
	if err != nil {
		return unavailable("codemap", op, err.Error()), nil
	}
	if res, ok := codemapError(op, stdout); ok {
		return res, nil
	}
	var r cmSemantic
	if derr := decodeJSON(stdout, &r); derr != nil {
		return degraded("codemap", op, stdout, stderr, code), nil
	}
	// Search hits are candidates, not proof (SPEC §5.2). find (name) is higher
	// confidence than semantic (meaning).
	conf := "medium"
	if op == "semantic" {
		conf = "low"
	}
	facts := make([]Fact, 0, len(r.Hits))
	for _, h := range r.Hits {
		facts = append(facts, Fact{Kind: "semantic_search", Confidence: conf,
			Claim:    fmt.Sprintf("%s (%s) at %s", h.Symbol, h.Kind, h.File),
			Location: &Location{File: h.File, StartLine: h.StartLine, EndLine: h.EndLine, Symbol: h.Symbol}})
	}
	return Result{
		Tool: "codemap", Operation: op, Status: StatusAuthoritative,
		Summary:  fmt.Sprintf("%s: %s for %q", op, pluralize(len(r.Hits), "candidate"), clip(query, 40)),
		Facts:    facts,
		Warnings: noteWarnings(r.Note, false, ""),
		Raw:      stdout,
	}, nil
}

// --- review ---

type cmReview struct {
	Indexed         bool            `json:"indexed"`
	IsRepo          bool            `json:"is_repo"`
	ChangedFiles    []cmChangedFile `json:"changed_files"`
	ChangedSymbols  []cmSymbolRef   `json:"changed_symbols"`
	BlastRadius     []cmSymbolRef   `json:"blast_radius"`
	CoveringTests   []cmSymbolRef   `json:"covering_tests"`
	UntestedSymbols []cmSymbolRef   `json:"untested_symbols"`
	Hotspots        []cmSymbolRef   `json:"hotspots"`
	Note            string          `json:"note"`
	Resolution      string          `json:"resolution"`
	CallGraph       string          `json:"call_graph"`
	Risk            *cmReviewRisk   `json:"risk"` // codemap ≥0.36 diff-scoped aggregate risk
}

// cmReviewRisk is codemap's diff-scoped aggregate risk band (worst across
// changed symbols). Absent (nil) when the diff maps to no indexed symbols.
type cmReviewRisk struct {
	Level   string         `json:"level"` // low|medium|high
	Score   float64        `json:"score"`
	Factors []cmRiskFactor `json:"factors"`
}

type cmRiskFactor struct {
	Factor   string  `json:"factor"` // untested_changes|hotspot_fanin|cross_package|ambiguity|unresolved
	Severity float64 `json:"severity"`
	Detail   string  `json:"detail"`
}

// cmChangedFile is one entry of codemap review's changed_files (an object, not
// a bare path string).
type cmChangedFile struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Symbols int    `json:"symbols"`
}

func (c *Codemap) review(ctx context.Context, dir, since string, staged bool) (Result, error) {
	args := []string{"review", "--json"}
	if staged {
		args = append(args, "--staged")
	}
	if since != "" {
		args = append(args, "--since", since)
	}
	stdout, stderr, code, err := c.exec(ctx, dir, args...)
	if err != nil {
		return unavailable("codemap", "review", err.Error()), nil
	}
	if res, ok := codemapError("review", stdout); ok {
		return res, nil
	}
	var r cmReview
	if derr := decodeJSON(stdout, &r); derr != nil {
		return degraded("codemap", "review", stdout, stderr, code), nil
	}
	claim := fmt.Sprintf("diff touches %s and %s; %s to run",
		pluralize(len(r.ChangedFiles), "file"),
		pluralize(len(r.ChangedSymbols), "symbol"),
		pluralize(len(r.CoveringTests), "covering test"))
	warns := noteWarnings(r.Note, false, "")
	if len(r.UntestedSymbols) > 0 {
		warns = append(warns, fmt.Sprintf("%s changed with no covering tests", pluralize(len(r.UntestedSymbols), "symbol")))
	}
	if len(r.Hotspots) > 0 {
		warns = append(warns, fmt.Sprintf("change touches %s (high fan-in)", pluralize(len(r.Hotspots), "hotspot")))
	}
	// A structural review is only authoritative when the project is indexed;
	// otherwise it is a plain changed-file list (blast radius unavailable).
	status, conf := StatusAuthoritative, "high"
	if !r.Indexed {
		status, conf = StatusPartial, "medium"
		claim = fmt.Sprintf("diff touches %s (codemap not indexed — no blast radius; run `codemap index`)", pluralize(len(r.ChangedFiles), "file"))
	}
	facts := []Fact{{Kind: "code_graph", Claim: claim, Confidence: conf}}
	// Diff-scoped aggregate risk band (codemap ≥0.36). A medium/high band warns
	// with its own factors so the §13.3 gate is grounded in the actual diff, not
	// just the case file's orient-time risk label. The warning carries a stable
	// "diff risk: <level>" prefix the kernel can key on.
	if r.Risk != nil && (r.Risk.Level == "medium" || r.Risk.Level == "high") {
		names := make([]string, 0, len(r.Risk.Factors))
		for _, f := range r.Risk.Factors {
			names = append(names, f.Factor)
		}
		riskClaim := fmt.Sprintf("diff risk: %s (%.2f) — %s", r.Risk.Level, r.Risk.Score, joinComma(names))
		facts = append(facts, Fact{Kind: "code_graph", Claim: riskClaim, Confidence: conf})
		warns = append(warns, riskClaim)
	}
	return Result{
		Tool: "codemap", Operation: "review", Status: status,
		Summary:  claim,
		Facts:    facts,
		Warnings: warns,
		Raw:      stdout,
	}, nil
}

// callGraphConfidence maps codemap's stable call_graph enum (≥0.36) to a
// confidence band: resolved→high, name→medium, unresolved/none→low. When the
// field is absent (old codemap), it falls back to the legacy resolution-sentence
// heuristic (empty/"precise"→high, else medium) so behavior is unchanged.
func callGraphConfidence(callGraph, resolution string) string {
	switch callGraph {
	case "resolved":
		return "high"
	case "name":
		return "medium"
	case "unresolved", "none":
		return "low"
	}
	if resolution != "precise" && resolution != "" {
		return "medium"
	}
	return "high"
}

// appendUnresolvedHint surfaces codemap's own remediation sentence (which carries
// "run codemap index --precise") when the call graph is unresolved, so the
// low-confidence downgrade is explained.
func appendUnresolvedHint(warns []string, callGraph, resolution string) []string {
	if callGraph == "unresolved" && resolution != "" {
		return append(warns, "codemap: "+clip(resolution, 200))
	}
	return warns
}

// noteWarnings turns codemap's honesty fields into warnings.
func noteWarnings(note string, untested bool, symbol string) []string {
	var w []string
	if note != "" {
		w = append(w, "codemap: "+note)
	}
	if untested {
		w = append(w, symbol+" has no covering tests")
	}
	return w
}

// degraded builds a partial result when a tool returned output we couldn't
// parse — we keep the raw (redacted) text as evidence rather than fabricate.
// The warning is reduced to the first non-empty line so a tool's multi-line
// usage dump doesn't flood the model context.
func degraded(tool, op, stdout, stderr string, code int) Result {
	msg := firstNonEmpty(firstLine(stderr), firstLine(stdout), "unparseable output")
	return Result{
		Tool: tool, Operation: op, Status: StatusPartial,
		Summary:  fmt.Sprintf("%s %s returned exit %d with output that could not be parsed as JSON", tool, op, code),
		Warnings: []string{tool + ": " + clip(msg, 140)},
		Raw:      firstNonEmpty(stdout, stderr),
	}
}
