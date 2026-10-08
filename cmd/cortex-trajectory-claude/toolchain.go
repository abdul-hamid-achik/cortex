/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

const (
	versionTimeout = 10 * time.Second
	maxVersionLen  = 256
)

// toolchainEntries lists every executable exposed to the arm. The cortex
// executable is included only in the cortex arm.
func toolchainEntries(opt options, arm trajectory.Arm) []toolSpec {
	entries := []toolSpec{{Name: "claude", Path: opt.Agent}}
	entries = append(entries, opt.Tools...)
	if arm == trajectory.ArmCortex && opt.Cortex != "" {
		entries = append(entries, toolSpec{Name: "cortex", Path: opt.Cortex})
	}
	return entries
}

// collectToolchain digests the symlink-resolved bytes of every exposed
// executable (the runner resolves and re-hashes independently) and asks each
// one for its version, in parallel.
func collectToolchain(ctx context.Context, entries []toolSpec) ([]trajectory.ToolchainProvenance, error) {
	scratch, err := os.MkdirTemp("", "cortex-traj-claude-version-")
	if err != nil {
		return nil, fmt.Errorf("create version scratch directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	provenance := make([]trajectory.ToolchainProvenance, len(entries))
	for i, entry := range entries {
		digest, err := resolvedDigest(entry.Path)
		if err != nil {
			return nil, fmt.Errorf("digest toolchain %q: %w", entry.Name, err)
		}
		provenance[i] = trajectory.ToolchainProvenance{Name: entry.Name, ExecutablePath: entry.Path, BinaryDigest: digest}
	}
	var wg sync.WaitGroup
	for i := range provenance {
		wg.Add(1)
		go func() {
			defer wg.Done()
			provenance[i].Version = executableVersion(ctx, provenance[i].ExecutablePath, scratch)
		}()
	}
	wg.Wait()
	return provenance, nil
}

func resolvedDigest(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	file, err := os.Open(resolved) // #nosec G304 -- operator-supplied executable path
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", resolved)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// executableVersion returns the first line of `<path> --version`, or
// "unknown". The probe runs in a private directory with a minimal environment.
func executableVersion(ctx context.Context, path, scratch string) string {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--version") // #nosec G204 -- operator-supplied executable path
	command.Dir = scratch
	command.Env = []string{"HOME=" + scratch, "TMPDIR=" + scratch, "PATH=" + childBasePath}
	var out bytes.Buffer
	command.Stdout = &limitedBuffer{buf: &out, limit: 4 << 10}
	command.WaitDelay = time.Second
	setProcessGroup(command)
	if err := command.Run(); err != nil {
		if command.Process != nil {
			killProcessGroup(command.Process.Pid)
		}
		return "unknown"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out.String()), "\n")
	line = strings.TrimSpace(line)
	if line == "" {
		return "unknown"
	}
	if len(line) > maxVersionLen {
		line = line[:maxVersionLen]
	}
	return strings.ToValidUTF8(line, "")
}

type limitedBuffer struct {
	buf   *bytes.Buffer
	limit int
}

func (l *limitedBuffer) Write(data []byte) (int, error) {
	if room := l.limit - l.buf.Len(); room > 0 {
		keep := len(data)
		if keep > room {
			keep = room
		}
		l.buf.Write(data[:keep])
	}
	return len(data), nil
}
