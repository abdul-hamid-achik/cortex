package trajectory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type fixtureLimits struct {
	maxFileBytes  int64
	maxTotalBytes int64
	maxEntries    int
	maxPathBytes  int
}

var defaultFixtureLimits = fixtureLimits{
	maxFileBytes:  maxFixtureFileBytes,
	maxTotalBytes: maxFixtureTotalBytes,
	maxEntries:    maxFixtureEntries,
	maxPathBytes:  maxFixturePathBytes,
}

type fixtureBudget struct {
	limits    fixtureLimits
	entries   int
	pathBytes int
	total     int64
}

func (b *fixtureBudget) add(relative string, info os.FileInfo) error {
	b.entries++
	if b.entries > b.limits.maxEntries {
		return fmt.Errorf("fixture exceeds the %d-entry limit", b.limits.maxEntries)
	}
	b.pathBytes += len(relative)
	if b.pathBytes > b.limits.maxPathBytes {
		return fmt.Errorf("fixture paths exceed the %d-byte aggregate limit", b.limits.maxPathBytes)
	}
	if info.IsDir() {
		return nil
	}
	if info.Size() > b.limits.maxFileBytes {
		return fmt.Errorf("fixture file %q exceeds the %d-byte limit", relative, b.limits.maxFileBytes)
	}
	if info.Size() > b.limits.maxTotalBytes-b.total {
		return fmt.Errorf("fixture exceeds the %d-byte total limit", b.limits.maxTotalBytes)
	}
	b.total += info.Size()
	return nil
}

func walkFixture(root string, limits fixtureLimits, visit func(path, relative string, info os.FileInfo) error) error {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("repository fixture root is a symlink")
	}
	if !rootInfo.IsDir() {
		return errors.New("repository fixture must be a directory")
	}
	budget := fixtureBudget{limits: limits}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("fixture path %q is a symlink", relative)
		}
		if entry.Name() == ".git" {
			return fmt.Errorf("fixture path %q contains forbidden git metadata", relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("fixture path %q is not a regular file", relative)
		}
		if err := budget.add(relative, info); err != nil {
			return err
		}
		return visit(path, relative, info)
	})
}

func streamFixtureFile(ctx context.Context, path string, expected os.FileInfo, limit int64, destination io.Writer) (written int64, err error) {
	file, err := openSnapshotFile(path)
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			written, err = 0, closeErr
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		return 0, fmt.Errorf("fixture file %q changed before it could be read", path)
	}
	reader := io.LimitReader(file, limit+1)
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		read, readErr := reader.Read(buffer)
		if read > 0 {
			written += int64(read)
			if written > limit {
				return 0, fmt.Errorf("fixture file %q exceeds the %d-byte limit", path, limit)
			}
			if _, err := destination.Write(buffer[:read]); err != nil {
				return 0, err
			}
		}
		switch {
		case errors.Is(readErr, io.EOF):
			final, err := file.Stat()
			if err != nil {
				return 0, err
			}
			if !os.SameFile(opened, final) || final.Size() != opened.Size() || final.ModTime() != opened.ModTime() || written != opened.Size() {
				return 0, fmt.Errorf("fixture file %q changed while it was read", path)
			}
			return written, nil
		case readErr != nil:
			return 0, readErr
		}
	}
}

func treeDigestWithLimits(root string, limits fixtureLimits) (string, error) {
	hash := sha256.New()
	err := walkFixture(root, limits, func(path, relative string, info os.FileInfo) error {
		if info.IsDir() {
			_, _ = io.WriteString(hash, "dir\x00"+relative+"\x00")
			return nil
		}
		_, _ = io.WriteString(hash, "file\x00"+relative+"\x00"+info.Mode().Perm().String()+"\x00")
		if _, err := streamFixtureFile(context.Background(), path, info, limits.maxFileBytes, hash); err != nil {
			return err
		}
		_, _ = hash.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func copyFixtureWithLimits(source, destination string, limits fixtureLimits) error {
	if err := secureMkdir(destination); err != nil {
		return err
	}
	return walkFixture(source, limits, func(path, relative string, info os.FileInfo) error {
		target := filepath.Join(destination, filepath.FromSlash(relative))
		if info.IsDir() {
			return os.Mkdir(target, 0o700)
		}
		if err := secureMkdir(filepath.Dir(target)); err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := streamFixtureFile(context.Background(), path, info, limits.maxFileBytes, output)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(target)
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}
		return os.Chmod(target, info.Mode().Perm())
	})
}

func copyFixtureFile(ctx context.Context, source, target string, limit int64) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("fixture file %q is not a regular file", source)
	}
	if info.Size() > limit {
		return fmt.Errorf("fixture file %q exceeds the %d-byte limit", source, limit)
	}
	if err := secureMkdir(filepath.Dir(target)); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".trajectory-copy-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := streamFixtureFile(ctx, source, info, limit, temporary); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if existing, err := os.Lstat(target); err == nil {
		if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() {
			_ = os.Remove(temporaryPath)
			return fmt.Errorf("oracle workspace target %q is not a regular file", target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func protectedFilesDigest(root string, paths []string) (string, error) {
	ordered := append([]string(nil), paths...)
	sort.Strings(ordered)
	hash := sha256.New()
	for _, relative := range ordered {
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("oracle protected path %q is not a regular file", relative)
		}
		if info.Size() > maxFixtureFileBytes {
			return "", fmt.Errorf("oracle protected path %q exceeds the %d-byte limit", relative, maxFixtureFileBytes)
		}
		_, _ = io.WriteString(hash, relative+"\x00"+info.Mode().Perm().String()+"\x00")
		if _, err := streamFixtureFile(context.Background(), path, info, maxFixtureFileBytes, hash); err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
