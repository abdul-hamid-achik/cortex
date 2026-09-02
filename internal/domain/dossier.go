package domain

import (
	"strings"
	"time"
)

// DossierKind classifies one repository-level memory entry.
type DossierKind string

const (
	DossierArchitecture DossierKind = "architecture"
	DossierInvariant    DossierKind = "invariant"
	DossierHotspot      DossierKind = "hotspot"
	DossierConvention   DossierKind = "convention"
	DossierQuestion     DossierKind = "question"
)

// DossierKinds is the documented vocabulary, in display order.
var DossierKinds = []string{
	string(DossierArchitecture), string(DossierInvariant), string(DossierHotspot),
	string(DossierConvention), string(DossierQuestion),
}

// Valid reports whether k is a documented dossier kind.
func (k DossierKind) Valid() bool {
	switch k {
	case DossierArchitecture, DossierInvariant, DossierHotspot, DossierConvention, DossierQuestion:
		return true
	default:
		return false
	}
}

// DossierEvidenceRef points at one evidence record in one case. Dossier entries
// outlive cases, so the reference carries both halves.
type DossierEvidenceRef struct {
	TaskID     string `json:"taskId"`
	EvidenceID string `json:"evidenceId"`
}

// DossierEntry is one durable, evidence-backed statement about a repository:
// what a module does, an invariant it keeps, a hotspot, a convention, or an
// open question. Commit records the HEAD the statement was true at; freshness
// checks mark the entry stale when its files changed after that commit.
type DossierEntry struct {
	ID          string               `json:"id"`
	Module      string               `json:"module"`
	Kind        DossierKind          `json:"kind"`
	Title       string               `json:"title"`
	Summary     string               `json:"summary"`
	Files       []string             `json:"files,omitempty"`
	Evidence    []DossierEvidenceRef `json:"evidence,omitempty"`
	Commit      string               `json:"commit,omitempty"`
	Stale       bool                 `json:"stale,omitempty"`
	StaleReason string               `json:"staleReason,omitempty"`
	Actor       string               `json:"actor,omitempty"`
	CreatedAt   time.Time            `json:"createdAt"`
	UpdatedAt   time.Time            `json:"updatedAt"`
}

// Validate enforces the entry invariants: a module, a documented kind, a
// title, and a summary.
func (e DossierEntry) Validate() error {
	if e.ID == "" {
		return errValidation("dossier entry has no id")
	}
	if strings.TrimSpace(e.Module) == "" {
		return errValidation("dossier entry has no module")
	}
	if !e.Kind.Valid() {
		return errValidation("dossier entry kind must be one of: " + strings.Join(DossierKinds, ", "))
	}
	if strings.TrimSpace(e.Title) == "" {
		return errValidation("dossier entry has no title")
	}
	if strings.TrimSpace(e.Summary) == "" {
		return errValidation("dossier entry has no summary")
	}
	if e.CreatedAt.IsZero() {
		return errValidation("dossier entry has no creation timestamp")
	}
	return nil
}

// Dossier is the per-repository memory document. It lives outside every case
// so the second session on a repository starts where the first one ended.
type Dossier struct {
	SchemaVersion int            `json:"schemaVersion"`
	Repository    string         `json:"repository"`
	UpdatedAt     time.Time      `json:"updatedAt"`
	Entries       []DossierEntry `json:"entries"`
}

// Fresh returns the entries not marked stale.
func (d Dossier) Fresh() []DossierEntry {
	out := make([]DossierEntry, 0, len(d.Entries))
	for _, e := range d.Entries {
		if !e.Stale {
			out = append(out, e)
		}
	}
	return out
}

// ForModule returns the entries recorded against one module path.
func (d Dossier) ForModule(module string) []DossierEntry {
	module = strings.Trim(strings.TrimSpace(module), "/")
	var out []DossierEntry
	for _, e := range d.Entries {
		if strings.Trim(e.Module, "/") == module {
			out = append(out, e)
		}
	}
	return out
}
