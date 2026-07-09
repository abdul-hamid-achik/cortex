package kernel

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/abdul-hamid-achik/cortex/internal/config"
)

// MigrateMove records one planned (dry run) or executed (apply) relocation of
// a legacy ~/.cortex entry into its XDG target.
type MigrateMove struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Skipped string `json:"skipped,omitempty"` // reason the move was NOT made
}

// MigrateReport is the result of a dry-run or applied `cortex migrate`.
type MigrateReport struct {
	Base        string        `json:"base"`
	Applied     bool          `json:"applied"`
	Moves       []MigrateMove `json:"moves"`
	RemovedBase bool          `json:"removedBase"`
	Note        string        `json:"note,omitempty"`
}

// Migrate moves a legacy ~/.cortex tree (config.yaml, sessions/, archive/,
// cache/, and anything else found there) into the split XDG locations
// (config.XDGConfigDir / XDGStateHome / XDGCacheHome). With apply=false it
// only plans the moves — a dry run that touches nothing on disk — so callers
// can show the user exactly what would happen before committing to it. With
// apply=true it performs the moves and, if the legacy base ends up empty,
// removes it too.
//
// A Note-only report (Moves is nil, nothing attempted) is returned — not an
// error — when there is nothing to migrate: $CORTEX_HOME is set explicitly
// (the base override is intentional; there is no XDG target to migrate to
// while it's set) or no legacy ~/.cortex directory exists (already on the
// split layout).
func Migrate(apply bool) (MigrateReport, error) {
	if os.Getenv(config.EnvHome) != "" {
		return MigrateReport{
			Note: "CORTEX_HOME is set explicitly; migrate targets a legacy ~/.cortex. Unset CORTEX_HOME to migrate to the XDG layout.",
		}, nil
	}
	base, ok := config.LegacyBase()
	if !ok {
		return MigrateReport{
			Note: "no legacy ~/.cortex to migrate — already on the XDG layout.",
		}, nil
	}

	entries, err := os.ReadDir(base)
	if err != nil {
		return MigrateReport{Base: base}, fmt.Errorf("reading legacy base %s: %w", base, err)
	}

	rep := MigrateReport{Base: base}
	for _, e := range entries {
		name := e.Name()
		from := filepath.Join(base, name)

		var destPath string
		switch name {
		case "config.yaml":
			destPath = filepath.Join(config.XDGConfigDir(), name)
		case "cache":
			// The legacy ~/.cortex/cache dir maps onto $XDG_CACHE_HOME/cortex
			// itself, not a "cache" subdirectory beneath it.
			destPath = config.XDGCacheHome()
		default:
			destPath = filepath.Join(config.XDGStateHome(), name)
		}

		mv := MigrateMove{From: from, To: destPath}
		if _, statErr := os.Stat(destPath); statErr == nil {
			mv.Skipped = "destination exists"
		}
		rep.Moves = append(rep.Moves, mv)
	}

	if !apply {
		return rep, nil
	}

	for i := range rep.Moves {
		mv := &rep.Moves[i]
		if mv.Skipped != "" {
			continue
		}
		if err := moveTree(mv.From, mv.To); err != nil {
			return rep, fmt.Errorf("moving %s to %s: %w", mv.From, mv.To, err)
		}
	}
	rep.Applied = true

	if remaining, err := os.ReadDir(base); err == nil && len(remaining) == 0 {
		if err := os.Remove(base); err == nil {
			rep.RemovedBase = true
		}
	}
	return rep, nil
}

// moveTree relocates src to dst, creating dst's parent directory first. It
// tries an atomic os.Rename; if that fails for ANY reason (not just
// cross-device — rename can fail in ways that differ across platforms,
// notably Windows) it falls back to a recursive copy followed by removing the
// source, which is correct (if slower) regardless of why the rename failed.
func moveTree(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyTree(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// copyTree recursively copies src (file, symlink, or directory) to dst,
// preserving file mode. This is moveTree's cross-device fallback, kept
// deliberately simple: it is only exercised when a plain rename fails.
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	return copyFile(src, dst, info.Mode())
}

// copyFile copies a single regular file, preserving mode.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
