package kernel

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

const (
	maxSeedFiles     = 8
	maxSeedFileBytes = 16 << 10 // 16 KiB per note
)

// seedFromNotes stamps bounded contents of note/packet files as orientation
// evidence. Paths may be absolute (e.g. vault notes) or workspace-relative.
// Failures degrade to warnings; they never block task start.
func (k *Kernel) seedFromNotes(c *domain.CaseFile, paths []string) (facts []domain.Evidence, warnings []string) {
	if c == nil || len(paths) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	n := 0
	for _, raw := range paths {
		if n >= maxSeedFiles {
			warnings = append(warnings, fmt.Sprintf("seed capped at %d notes", maxSeedFiles))
			break
		}
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(k.cfg.Workspace, path)
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		data, err := os.ReadFile(path) // #nosec G304 -- operator-supplied seed path
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("seed note unreadable: %s", filepath.Base(path)))
			continue
		}
		if len(data) == 0 {
			continue
		}
		if len(data) > maxSeedFileBytes {
			data = data[:maxSeedFileBytes]
			warnings = append(warnings, fmt.Sprintf("seed note truncated to %d KiB: %s", maxSeedFileBytes>>10, filepath.Base(path)))
		}
		body := k.red.String(string(data))
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		claim := fmt.Sprintf("seed note %s: %s", filepath.Base(path), clipStr(singleLine(body), 240))
		sum := sha256.Sum256([]byte(path))
		stableID := fmt.Sprintf("ev_seed_%x", sum[:8])
		ev, err := k.stampEvidenceOnce(c.ID, stableID, "note", adapters.Fact{
			Kind:       "model_inference",
			Claim:      claim,
			Confidence: "medium",
			URI:        path,
		}, c.CreatedAt)
		if err != nil {
			warnings = append(warnings, "seed note stamp failed: "+filepath.Base(path))
			continue
		}
		facts = append(facts, ev)
		n++
	}
	if n > 0 {
		warnings = append(warnings, fmt.Sprintf("seeded %s from notes/packets into orientation evidence", pluralizeEv(n)))
	}
	return facts, warnings
}

// normalizeSeedPaths validates and bounds seed path lists for open/start.
func normalizeSeedPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if len(paths) > maxSeedFiles {
		return nil, fmt.Errorf("seed accepts at most %d note paths", maxSeedFiles)
	}
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if textExceeds(p, maxLocatorBytes) {
			return nil, fmt.Errorf("seed path exceeds %d bytes", maxLocatorBytes)
		}
		key := filepath.Clean(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out, nil
}
