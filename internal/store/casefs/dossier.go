package casefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// maxDossierEntries bounds one repository's memory document.
const maxDossierEntries = 2048

// RepoStore persists repository-level memory (the dossier) outside every case
// directory, so it survives case completion and archival. It reuses the case
// store's cross-process lock so two sessions cannot interleave writes.
type RepoStore struct {
	root string
	lock *Store
}

// NewRepoStore opens (creating on first write) the memory directory for one
// repository slug under root, e.g. $XDG_STATE_HOME/cortex/repos/<slug>.
func NewRepoStore(root string) (*RepoStore, error) {
	if root == "" {
		return nil, errors.New("repo store needs a root")
	}
	return &RepoStore{root: root, lock: &Store{root: root}}, nil
}

// Root returns the repository memory directory.
func (r *RepoStore) Root() string { return r.root }

func (r *RepoStore) dossierPath() string { return filepath.Join(r.root, "dossier.json") }

// LoadDossier reads the repository dossier; an absent document is empty.
func (r *RepoStore) LoadDossier() (domain.Dossier, error) {
	return r.loadUnlocked()
}

func (r *RepoStore) loadUnlocked() (domain.Dossier, error) {
	d := domain.Dossier{SchemaVersion: 1}
	if err := readJSON(r.dossierPath(), &d); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.Dossier{SchemaVersion: 1}, nil
		}
		return domain.Dossier{}, err
	}
	return d, nil
}

// UpdateDossier mutates the dossier under the repository lock, validates every
// entry, stamps UpdatedAt, and writes both the JSON document and a rendered
// dossier.md for humans.
func (r *RepoStore) UpdateDossier(now time.Time, mutate func(*domain.Dossier) error, render func(domain.Dossier) string) (domain.Dossier, error) {
	var out domain.Dossier
	err := r.lock.withTaskLockNoRecovery("coord_dossier", func() error {
		d, err := r.loadUnlocked()
		if err != nil {
			return err
		}
		if err := mutate(&d); err != nil {
			return err
		}
		if len(d.Entries) > maxDossierEntries {
			return fmt.Errorf("dossier exceeds %d entries", maxDossierEntries)
		}
		for _, e := range d.Entries {
			if err := e.Validate(); err != nil {
				return err
			}
		}
		d.SchemaVersion = 1
		d.UpdatedAt = now.UTC()
		if err := writeJSON(r.dossierPath(), d); err != nil {
			return err
		}
		if render != nil {
			if err := writeFileAtomic(filepath.Join(r.root, "dossier.md"), []byte(render(d)), 0o600); err != nil {
				return err
			}
		}
		out = d
		return nil
	})
	return out, err
}
