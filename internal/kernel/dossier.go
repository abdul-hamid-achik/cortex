package kernel

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

const (
	maxDossierSummaryBytes  = 4 << 10
	maxDossierFiles         = 32
	maxDossierEvidence      = 32
	maxDossierOrientation   = 5
	maxDossierListDefault   = 50
	dossierOrientationClaim = 160
)

// DossierEntryInput creates or updates one repository memory entry from an
// active case. Evidence IDs must exist in that case; the entry stores the
// (task, evidence) pair so provenance survives case completion.
type DossierEntryInput struct {
	TaskID   string
	EntryID  string // update an existing entry when set
	Module   string
	Kind     string // architecture | invariant | hotspot | convention | question
	Title    string
	Summary  string
	Files    []string
	Evidence []string
	Actor    string
}

// DossierListInput filters the dossier view.
type DossierListInput struct {
	Module    string
	Kind      string
	StaleOnly bool
	Limit     int
}

// DossierReport is the result of every dossier operation.
type DossierReport struct {
	domain.Envelope
	Repository string                `json:"repository"`
	Path       string                `json:"path"`
	Entry      *domain.DossierEntry  `json:"entry,omitempty"`
	Entries    []domain.DossierEntry `json:"entries,omitempty"`
	Total      int                   `json:"total"`
	Stale      int                   `json:"stale"`
	UpdatedAt  *time.Time            `json:"updatedAt,omitempty"`
}

// RepoMemoryDir is where a workspace's dossier lives: always the central
// state tree, independent of a custom cases_dir, so repository memory is one
// place per repository.
func RepoMemoryDir(workspace string) string {
	return filepath.Join(config.StateHome(), "repos", config.Slug(workspace))
}

func (k *Kernel) repoStore() (*casefs.RepoStore, error) {
	return casefs.NewRepoStore(RepoMemoryDir(k.cfg.Workspace))
}

func dossierEnumAction(taskID, field string, values []string) domain.NextAction {
	return domain.NextAction{
		Tool: "cortex_dossier", Command: cortexCommand(nil, "dossier", "add", taskID, "--"+field, strings.ToUpper(field)),
		Reason: "use a documented " + field, Arguments: map[string]any{"taskId": taskID, "operation": "add"},
		Inputs: []string{field}, Candidates: map[string][]string{field: values},
	}
}

// UpsertDossierEntry writes one evidence-backed statement into the
// repository dossier and stamps a matching model_inference record into the
// owning case, so the case ledger shows what was promoted to durable memory.
func (k *Kernel) UpsertDossierEntry(ctx context.Context, in DossierEntryInput) (DossierReport, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return DossierReport{Envelope: errEnvelope(in.TaskID, err.Error())}, nil
	}
	if c.Status.IsTerminal() {
		return DossierReport{Envelope: k.errEnvelopeForCase(c, fmt.Sprintf("cannot write dossier entries from terminal phase %q", c.Status))}, nil
	}
	module := domain.NormalizeModulePath(in.Module)
	if strings.TrimSpace(in.Module) == "" {
		return DossierReport{Envelope: k.errEnvelopeActions(c.ID, "dossier entry needs a module path", domain.NextAction{
			Tool: "cortex_dossier", Command: cortexCommand(c, "dossier", "add", c.ID, "--module", "MODULE"),
			Reason: "name the module or directory the entry describes", Arguments: cloneArgs(knownActionArgs(c), "operation", "add"),
			Inputs: []string{"module"}, Candidates: map[string][]string{"module": k.coverageModuleCandidates(c.ID)}})}, nil
	}
	kind := domain.DossierKind(strings.ToLower(strings.TrimSpace(in.Kind)))
	if kind == "" {
		kind = domain.DossierArchitecture
	}
	if !kind.Valid() {
		return DossierReport{Envelope: k.errEnvelopeActions(c.ID, "dossier kind must be one of: "+strings.Join(domain.DossierKinds, ", "), dossierEnumAction(c.ID, "kind", domain.DossierKinds))}, nil
	}
	title := strings.TrimSpace(in.Title)
	summary := strings.TrimSpace(in.Summary)
	if title == "" || summary == "" {
		return DossierReport{Envelope: k.errEnvelopeActions(c.ID, "dossier entry needs a title and a summary", domain.NextAction{
			Tool: "cortex_dossier", Command: cortexCommand(c, "dossier", "add", c.ID, "--module", module, "--title", "TITLE", "--summary", "SUMMARY"),
			Reason: "state what the module does or which invariant it keeps", Arguments: cloneArgs(knownActionArgs(c), "operation", "add", "module", module),
			Inputs: []string{"title", "summary"}})}, nil
	}
	if textExceeds(title, maxLocatorBytes) || textExceeds(summary, maxDossierSummaryBytes) {
		return DossierReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("dossier title is bounded at %d bytes and summary at %d bytes", maxLocatorBytes, maxDossierSummaryBytes))}, nil
	}
	if len(in.Files) > maxDossierFiles || len(in.Evidence) > maxDossierEvidence {
		return DossierReport{Envelope: errEnvelope(c.ID, fmt.Sprintf("a dossier entry accepts at most %d files and %d evidence ids", maxDossierFiles, maxDossierEvidence))}, nil
	}
	evidenceIDs, missing := k.knownEvidenceIDs(c.ID, in.Evidence)
	if len(missing) > 0 {
		return DossierReport{Envelope: k.errEnvelopeActions(c.ID, "dossier entry cites evidence that does not exist in this case: "+strings.Join(missing, ", "),
			domain.NextAction{Tool: "cortex_dossier", Command: cortexCommand(c, "dossier", "add", c.ID, "--module", module),
				Reason: "cite only evidence ids recorded in this case", Arguments: cloneArgs(knownActionArgs(c), "operation", "add", "module", module),
				Inputs: []string{"evidence"}, Candidates: map[string][]string{"evidence": k.evidenceIDCandidates(c.ID)}})}, nil
	}
	if k.red.Detected(title) || k.red.Detected(summary) || k.red.Detected(module) {
		return DossierReport{Envelope: errEnvelope(c.ID, "dossier entry looks like it contains a secret; repository memory never stores sensitive text")}, nil
	}
	files := make([]string, 0, len(in.Files))
	for _, f := range in.Files {
		if f = strings.TrimSpace(f); f != "" {
			if k.red.Detected(f) {
				return DossierReport{Envelope: errEnvelope(c.ID, "dossier file path looks sensitive; repository memory never stores sensitive text")}, nil
			}
			files = append(files, k.workspaceRelative(f))
		}
	}
	if len(files) == 0 {
		files = []string{module}
	}
	refs := make([]domain.DossierEvidenceRef, 0, len(evidenceIDs))
	for _, id := range evidenceIDs {
		refs = append(refs, domain.DossierEvidenceRef{TaskID: c.ID, EvidenceID: id})
	}
	repo, err := k.repoStore()
	if err != nil {
		return DossierReport{Envelope: errEnvelope(c.ID, err.Error())}, err
	}
	now := k.now().UTC()
	head := k.headCommit(ctx)
	actor := k.red.String(strings.TrimSpace(in.Actor))
	var written domain.DossierEntry
	d, err := repo.UpdateDossier(now, func(d *domain.Dossier) error {
		if d.Repository == "" {
			d.Repository = c.Workspace.Repository
		}
		if id := strings.TrimSpace(in.EntryID); id != "" {
			for i := range d.Entries {
				if d.Entries[i].ID != id {
					continue
				}
				e := &d.Entries[i]
				e.Module, e.Kind, e.Title, e.Summary, e.Files = module, kind, title, summary, files
				e.Evidence = refs
				e.Commit, e.Stale, e.StaleReason = head, false, ""
				e.Actor, e.UpdatedAt = actor, now
				written = *e
				return nil
			}
			return fmt.Errorf("dossier entry %s not found", id)
		}
		written = domain.DossierEntry{
			ID: ids.New("dos"), Module: module, Kind: kind, Title: title, Summary: summary,
			Files: files, Evidence: refs, Commit: head, Actor: actor, CreatedAt: now, UpdatedAt: now,
		}
		d.Entries = append(d.Entries, written)
		return nil
	}, renderDossierMarkdown)
	if err != nil {
		return DossierReport{Envelope: errEnvelope(c.ID, err.Error())}, nil
	}
	// The case ledger records the promotion so a later reader sees the
	// conclusion next to the evidence it rests on.
	fact := adapters.Fact{Kind: "model_inference", Confidence: "medium",
		Claim:    fmt.Sprintf("dossier[%s] %s — %s: %s", written.ID, module, title, clipStr(summary, 240)),
		Location: &adapters.Location{File: module}}
	if ev, err := k.stampEvidenceDerived(c.ID, "dossier", fact, "", evidenceIDs); err == nil {
		_ = ev
	}
	k.markModuleSummarized(c.ID, module, written.ID)
	stale := 0
	for _, e := range d.Entries {
		if e.Stale {
			stale++
		}
	}
	rep := DossierReport{
		Envelope: domain.Envelope{OK: true, TaskID: c.ID, Phase: c.Status,
			Summary:     fmt.Sprintf("dossier entry %s written for %s (%s): %s", written.ID, module, kind, clipStr(title, 80)),
			NextActions: nextForPhase(c.Status)},
		Repository: d.Repository, Path: repo.Root(), Entry: &written, Total: len(d.Entries), Stale: stale, UpdatedAt: &d.UpdatedAt,
	}
	k.attachStructuredActions(&rep.Envelope, c)
	k.checkpoint(c.ID)
	return rep, nil
}

// Dossier returns the repository memory, freshness evaluated live (not
// persisted — RefreshDossier writes the stale marks).
func (k *Kernel) Dossier(ctx context.Context, in DossierListInput) (DossierReport, error) {
	repo, err := k.repoStore()
	if err != nil {
		return DossierReport{Envelope: errEnvelope("", err.Error())}, err
	}
	d, err := repo.LoadDossier()
	if err != nil {
		return DossierReport{Envelope: errEnvelope("", err.Error())}, err
	}
	kind := domain.DossierKind(strings.ToLower(strings.TrimSpace(in.Kind)))
	if kind != "" && !kind.Valid() {
		return DossierReport{Envelope: k.errEnvelopeActions("", "dossier kind must be one of: "+strings.Join(domain.DossierKinds, ", "), dossierEnumAction("TASK_ID", "kind", domain.DossierKinds))}, nil
	}
	probe := k.newFreshnessProbe(ctx)
	stale := 0
	for i := range d.Entries {
		if s, reason := probe.stale(d.Entries[i].Commit, d.Entries[i].Files); s {
			d.Entries[i].Stale, d.Entries[i].StaleReason = true, reason
		}
		if d.Entries[i].Stale {
			stale++
		}
	}
	module := ""
	if strings.TrimSpace(in.Module) != "" {
		module = domain.NormalizeModulePath(in.Module)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = maxDossierListDefault
	}
	entries := make([]domain.DossierEntry, 0, len(d.Entries))
	for _, e := range d.Entries {
		if module != "" && domain.NormalizeModulePath(e.Module) != module && !strings.HasPrefix(domain.NormalizeModulePath(e.Module), module+"/") {
			continue
		}
		if kind != "" && e.Kind != kind {
			continue
		}
		if in.StaleOnly && !e.Stale {
			continue
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].UpdatedAt.After(entries[j].UpdatedAt) })
	truncated := false
	if len(entries) > limit {
		entries = entries[:limit]
		truncated = true
	}
	rep := DossierReport{
		Envelope:   domain.Envelope{OK: true, Summary: fmt.Sprintf("dossier for %s: %d entries (%d stale)", firstNonEmptyStr(d.Repository, config.Slug(k.cfg.Workspace)), len(d.Entries), stale)},
		Repository: d.Repository, Path: repo.Root(), Entries: entries, Total: len(d.Entries), Stale: stale,
	}
	if !d.UpdatedAt.IsZero() {
		rep.UpdatedAt = &d.UpdatedAt
	}
	rep.Warnings = append(rep.Warnings, probe.warnings...)
	if truncated {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("dossier view bounded to %d entries; filter by module or kind", limit))
	}
	if stale > 0 {
		rep.Actions = append(rep.Actions, domain.NextAction{
			Tool: "cortex_dossier", Command: cortexCommand(nil, "dossier", "refresh"),
			Reason:    fmt.Sprintf("%d entries describe files that changed since they were written; re-read them before trusting", stale),
			Arguments: map[string]any{"operation": "refresh", "workspace": k.cfg.Workspace},
		})
	}
	return rep, nil
}

// RefreshDossier recomputes freshness and persists the stale marks.
func (k *Kernel) RefreshDossier(ctx context.Context) (DossierReport, error) {
	repo, err := k.repoStore()
	if err != nil {
		return DossierReport{Envelope: errEnvelope("", err.Error())}, err
	}
	probe := k.newFreshnessProbe(ctx)
	if !probe.available() {
		return DossierReport{Envelope: errEnvelope("", "cannot refresh the dossier: git HEAD is unavailable")}, nil
	}
	stale := 0
	d, err := repo.UpdateDossier(k.now(), func(d *domain.Dossier) error {
		for i := range d.Entries {
			s, reason := probe.stale(d.Entries[i].Commit, d.Entries[i].Files)
			d.Entries[i].Stale, d.Entries[i].StaleReason = s, reason
			if s {
				stale++
			}
		}
		return nil
	}, renderDossierMarkdown)
	if err != nil {
		return DossierReport{Envelope: errEnvelope("", err.Error())}, nil
	}
	rep := DossierReport{
		Envelope:   domain.Envelope{OK: true, Summary: fmt.Sprintf("dossier refreshed: %d entries, %d stale", len(d.Entries), stale), Warnings: probe.warnings},
		Repository: d.Repository, Path: repo.Root(), Total: len(d.Entries), Stale: stale, UpdatedAt: &d.UpdatedAt,
	}
	return rep, nil
}

// dossierOrientationFacts stamps the freshest non-stale dossier entries as
// low-confidence orientation evidence so a new case starts from repository
// memory instead of re-deriving it. Retry-stable ids keep re-orientation
// idempotent.
func (k *Kernel) dossierOrientationFacts(ctx context.Context, c *domain.CaseFile) ([]domain.Evidence, int) {
	repo, err := k.repoStore()
	if err != nil {
		return nil, 0
	}
	d, err := repo.LoadDossier()
	if err != nil || len(d.Entries) == 0 {
		return nil, 0
	}
	probe := k.newFreshnessProbe(ctx)
	fresh := make([]domain.DossierEntry, 0, len(d.Entries))
	for _, e := range d.Entries {
		if e.Stale {
			continue
		}
		if s, _ := probe.stale(e.Commit, e.Files); s {
			continue
		}
		fresh = append(fresh, e)
	}
	sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].UpdatedAt.After(fresh[j].UpdatedAt) })
	var facts []domain.Evidence
	for i, e := range fresh {
		if i >= maxDossierOrientation {
			break
		}
		claim := fmt.Sprintf("repo memory [%s] %s — %s: %s", e.Kind, e.Module, e.Title, clipStr(e.Summary, dossierOrientationClaim))
		stableID := "ev_orientation_dossier_" + strings.TrimPrefix(strings.ToLower(e.ID), "dos_")
		if ev, err := k.stampEvidenceOnce(c.ID, stableID, "dossier", adapters.Fact{
			Kind: "model_inference", Confidence: "low", Claim: claim, Location: &adapters.Location{File: e.Module},
		}, c.CreatedAt); err == nil {
			facts = append(facts, ev)
		}
	}
	return facts, len(fresh)
}

// renderDossierMarkdown produces the human-readable dossier.md.
func renderDossierMarkdown(d domain.Dossier) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Repository dossier: %s\n\n", d.Repository)
	if !d.UpdatedAt.IsZero() {
		fmt.Fprintf(&b, "_Updated %s · %d entries_\n\n", d.UpdatedAt.Format(time.RFC3339), len(d.Entries))
	}
	byModule := map[string][]domain.DossierEntry{}
	var modules []string
	for _, e := range d.Entries {
		if _, ok := byModule[e.Module]; !ok {
			modules = append(modules, e.Module)
		}
		byModule[e.Module] = append(byModule[e.Module], e)
	}
	sort.Strings(modules)
	for _, m := range modules {
		fmt.Fprintf(&b, "## %s\n\n", m)
		for _, e := range byModule[m] {
			marker := ""
			if e.Stale {
				marker = " ⚠ stale"
				if e.StaleReason != "" {
					marker += " (" + e.StaleReason + ")"
				}
			}
			fmt.Fprintf(&b, "- **[%s] %s**%s — %s", e.Kind, e.Title, marker, singleLine(e.Summary))
			if e.Commit != "" {
				fmt.Fprintf(&b, " _(@%s)_", clipStr(e.Commit, 12))
			}
			if len(e.Evidence) > 0 {
				refs := make([]string, 0, len(e.Evidence))
				for _, r := range e.Evidence {
					refs = append(refs, r.TaskID+"/"+r.EvidenceID)
				}
				fmt.Fprintf(&b, " · evidence: %s", strings.Join(clipList(refs, 4), ", "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
