package adapters

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Vidtrace adapts the vidtrace CLI: it turns bug videos into timestamped
// evidence bundles (frames/OCR/transcript/timeline) and links a visible failure
// moment to the code that likely owns it (SPEC §19.4 "investigate a bug video").
// It is the video-specialized companion to fcheap. vidtrace uses Go-style flags
// (both -flag and --flag are accepted) and emits `--json` as its contract.
type Vidtrace struct{ tool }

// NewVidtrace builds a vidtrace adapter. Media extraction can be slow, so the
// timeout is generous — matching the SPEC §17.2 artifact/browser budgets.
func NewVidtrace() *Vidtrace { return &Vidtrace{tool: newTool("vidtrace", 180*time.Second)} }

func (v *Vidtrace) Name() string { return "vidtrace" }

func (v *Vidtrace) Capabilities() []Capability { return []Capability{CapabilityArtifacts} }

// Health runs `vidtrace doctor -json` (checks ffmpeg/ffprobe/tesseract/whisper).
func (v *Vidtrace) Health(ctx context.Context) error {
	if !binExists(v.bin) {
		return ErrToolMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	_, _, _, err := v.run.run(ctx, "", v.bin, "--version")
	return err
}

// Execute routes vidtrace operations.
func (v *Vidtrace) Execute(ctx context.Context, req Request) (Result, error) {
	if !binExists(v.bin) {
		return unavailable("vidtrace", req.Operation, "not on PATH"), nil
	}
	dir := req.Str("dir")
	switch req.Operation {
	case "investigate":
		return v.investigate(ctx, dir, req)
	case "stash_list":
		return v.stashList(ctx, dir)
	default:
		return Result{Tool: "vidtrace", Operation: req.Operation, Status: StatusError,
			Summary: "unknown vidtrace operation: " + req.Operation}, nil
	}
}

// vtMatch is a code match vidtrace resolves via --connect (fcheap/vecgrep). The
// real 0.15 shape is {file, line, text, score, source}: `file` is a pure source
// path and `line` is a 1-based anchor (omitted when unknown). path/snippet are
// kept as fallbacks for version drift.
type vtMatch struct {
	File    string  `json:"file"`
	Path    string  `json:"path"`
	Text    string  `json:"text"`
	Snippet string  `json:"snippet"`
	Line    int     `json:"line"`
	Score   float64 `json:"score"`
	Source  string  `json:"source"`
}

// vtEvidence is one visible-failure frame from the bug video (the base
// investigate output, without --connect).
type vtEvidence struct {
	Score       float64 `json:"score"`
	TimeSeconds float64 `json:"time_seconds"`
	Frame       string  `json:"frame"`
	OCR         string  `json:"ocr"`
	SourceVideo string  `json:"source_video"`
	EvidenceID  string  `json:"evidence_id"`
}

// vtInvestigate mirrors the real `vidtrace investigate --json` contract:
// {ok, error?, query, bundle_dir, evidence:[…frames…]} plus code matches from
// --connect (field name varies by version, so several are accepted). A false
// `ok` carries an `error` string.
type vtInvestigate struct {
	OK          bool         `json:"ok"`
	Error       string       `json:"error"`
	Query       string       `json:"query"`
	BundleDir   string       `json:"bundle_dir"`
	Summary     string       `json:"summary"`
	Evidence    []vtEvidence `json:"evidence"`
	CodeMatches []vtMatch    `json:"code_matches"` // the real --connect field
	Matches     []vtMatch    `json:"matches"`      // fallbacks for version drift
	Connect     []vtMatch    `json:"connect"`
}

// codeMatches returns whichever --connect field the version populated.
func (r vtInvestigate) codeMatches() []vtMatch {
	switch {
	case len(r.CodeMatches) > 0:
		return r.CodeMatches
	case len(r.Matches) > 0:
		return r.Matches
	default:
		return r.Connect
	}
}

func (v *Vidtrace) investigate(ctx context.Context, dir string, req Request) (Result, error) {
	query := req.Str("query")
	bundle := req.Str("bundle")
	stash := req.Str("stash")
	if query == "" || (bundle == "" && stash == "") {
		return Result{Tool: "vidtrace", Operation: "investigate", Status: StatusError,
			Summary: "investigate needs a query and either a bundle path or a stash id"}, nil
	}
	codebase := firstNonEmpty(req.Str("codebase"), dir)
	args := []string{"investigate"}
	if stash != "" {
		args = append(args, "--stash", stash)
	} else {
		args = append(args, bundle)
	}
	args = append(args, "--query", query)
	if codebase != "" {
		args = append(args, "--codebase", codebase)
	}
	// --connect asks vidtrace to resolve real code matches (via fcheap/vecgrep).
	// It requires a codebase; vidtrace 0.15 rejects a bare --connect with exit 2,
	// which would sink the whole investigation (losing the video frames too), so
	// only request it when we have a codebase to resolve against.
	if boolOf(req.Input["connect"]) && codebase != "" {
		args = append(args, "--connect")
		args = append(args, "--connect-limit", strconv.Itoa(req.Int("limit", 10)))
	}
	args = append(args, "--json")

	stdout, stderr, code, err := v.exec(ctx, dir, args...)
	if err != nil {
		return unavailable("vidtrace", "investigate", err.Error()), nil
	}
	var r vtInvestigate
	if derr := decodeJSON(stdout, &r); derr != nil {
		return degraded("vidtrace", "investigate", stdout, stderr, code), nil
	}
	// vidtrace reports failures in-band with ok:false + error (e.g. an invalid
	// bundle). Surface that as partial, never a fabricated success.
	if !r.OK {
		return Result{Tool: "vidtrace", Operation: "investigate", Status: StatusPartial,
			Summary:  "vidtrace could not investigate the bundle",
			Warnings: []string{"vidtrace: " + firstNonEmpty(r.Error, "investigation failed")},
			Raw:      stdout}, nil
	}
	ref := "vidtrace://" + firstNonEmpty(bundle, stash, r.BundleDir)
	matches := r.codeMatches()
	facts := make([]Fact, 0, len(r.Evidence)+len(matches)+1)
	facts = append(facts, Fact{Kind: "artifact", Confidence: "low", URI: ref,
		Claim: fmt.Sprintf("bug video for %q: %s of visible evidence, %s of owning-code candidate",
			clip(query, 40), pluralize(len(r.Evidence), "frame"), pluralize(len(matches), "candidate"))})
	// Visible-failure frames (the video-derived evidence).
	for _, e := range r.Evidence {
		facts = append(facts, Fact{Kind: "artifact", Confidence: "low", URI: ref,
			Claim: fmt.Sprintf("at %.0fs the video shows: %s", e.TimeSeconds, clip(firstLine(e.OCR), 90))})
	}
	// Owning-code candidates from --connect, when present. vidtrace 0.15's
	// code_matches carry a pure file path plus a 1-based line, so the fact anchors
	// at file:line; older output without a line degrades to file-only.
	for _, m := range matches {
		path := firstNonEmpty(m.File, m.Path)
		where := path
		if m.Line > 0 {
			where = fmt.Sprintf("%s:%d", path, m.Line)
		}
		claim := fmt.Sprintf("video failure likely owned by %s (score %.2f)", where, m.Score)
		if snip := firstLine(firstNonEmpty(m.Text, m.Snippet)); snip != "" {
			claim += ": " + clip(snip, 80)
		}
		facts = append(facts, Fact{Kind: "artifact", Confidence: "low", URI: ref,
			Claim: claim, Location: &Location{File: path, StartLine: m.Line}})
	}
	return Result{
		Tool: "vidtrace", Operation: "investigate", Status: StatusAuthoritative,
		Summary: fmt.Sprintf("investigated bug video for %q: %s, %s",
			clip(query, 40), pluralize(len(r.Evidence), "evidence frame"), pluralize(len(matches), "code candidate")),
		Facts: facts,
		Raw:   stdout,
	}, nil
}

type vtStash struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Tool string `json:"tool"`
}

func (v *Vidtrace) stashList(ctx context.Context, dir string) (Result, error) {
	stdout, stderr, code, err := v.exec(ctx, dir, "stash", "list", "--tool", "vidtrace", "--json")
	if err != nil {
		return unavailable("vidtrace", "stash_list", err.Error()), nil
	}
	// `vidtrace stash list --json` wraps the array: {ok, error?, stashes:[…]}.
	var out struct {
		OK      bool      `json:"ok"`
		Error   string    `json:"error"`
		Stashes []vtStash `json:"stashes"`
	}
	if derr := decodeJSON(stdout, &out); derr != nil {
		return degraded("vidtrace", "stash_list", stdout, stderr, code), nil
	}
	// vidtrace reports failures in-band with ok:false + error (mirroring the
	// investigate path). Surface that as partial, never a fabricated "0 bundles".
	if !out.OK {
		return Result{Tool: "vidtrace", Operation: "stash_list", Status: StatusPartial,
			Summary:  "vidtrace could not list bug-video stashes",
			Warnings: []string{"vidtrace: " + firstNonEmpty(out.Error, "stash list failed")},
			Raw:      stdout}, nil
	}
	stashes := out.Stashes
	arts := make([]ArtifactRef, 0, len(stashes))
	facts := make([]Fact, 0, len(stashes))
	for _, s := range stashes {
		arts = append(arts, ArtifactRef{ID: s.ID, Kind: "bug-video", URI: "vidtrace://" + s.ID,
			Summary: firstNonEmpty(s.Name, s.ID)})
		facts = append(facts, Fact{Kind: "artifact", Confidence: "low", URI: "vidtrace://" + s.ID,
			Claim: fmt.Sprintf("prior bug-video bundle %q (%s) — investigate it with --video %s", firstNonEmpty(s.Name, s.ID), s.ID, s.ID)})
	}
	return Result{
		Tool: "vidtrace", Operation: "stash_list", Status: StatusAuthoritative,
		Summary:   pluralize(len(stashes), "archived bug-video bundle"),
		Facts:     facts,
		Artifacts: arts,
		Raw:       stdout,
	}, nil
}
