package kernel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/config"
)

// KnownStore is one extra case-store root outside the central XDG sessions tree.
// Cortex records these when a kernel opens a custom cases_dir so Studio and
// AllSessions can see repo-local work without a drifting session index.
type KnownStore struct {
	Root string `json:"root"`
	Slug string `json:"slug"`
}

func knownStoresPath() string {
	return filepath.Join(config.StateHome(), "known-stores.json")
}

// RegisterKnownStore records a custom case-store root. Central session trees are
// ignored (already walked). Failures are silent: a missing registry must never
// block opening a task.
func RegisterKnownStore(root, slug string) {
	root = filepath.Clean(strings.TrimSpace(root))
	slug = strings.TrimSpace(slug)
	if root == "" || slug == "" || root == "." || root == string(filepath.Separator) {
		return
	}
	central := filepath.Clean(config.SessionsRoot())
	if root == central || strings.HasPrefix(root, central+string(filepath.Separator)) {
		return
	}
	stores := loadKnownStores()
	for i, existing := range stores {
		if existing.Root == root {
			stores[i].Slug = slug
			_ = writeKnownStores(stores)
			return
		}
	}
	stores = append(stores, KnownStore{Root: root, Slug: slug})
	_ = writeKnownStores(stores)
}

func loadKnownStores() []KnownStore {
	data, err := os.ReadFile(knownStoresPath())
	if err != nil {
		return nil
	}
	var stores []KnownStore
	if json.Unmarshal(data, &stores) != nil {
		return nil
	}
	out := make([]KnownStore, 0, len(stores))
	seen := map[string]bool{}
	for _, store := range stores {
		root := filepath.Clean(strings.TrimSpace(store.Root))
		slug := strings.TrimSpace(store.Slug)
		if root == "" || slug == "" || seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, KnownStore{Root: root, Slug: slug})
	}
	return out
}

func writeKnownStores(stores []KnownStore) error {
	path := knownStoresPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(stores)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
