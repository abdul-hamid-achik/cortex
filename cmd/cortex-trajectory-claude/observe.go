/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

const (
	maxCaseFileBytes   = 8 << 20
	maxLedgerLine      = 1 << 20
	maxObservedCases   = 256
	maxObservedDepth   = 4
	caseFileName       = "case.json"
	evidenceFileName   = "evidence.jsonl"
	hypothesesFileName = "hypotheses.json"
)

// observeCases derives the instrumented observation from the case files the
// agent's Cortex server wrote under the per-arm case store. Absence and
// corruption are tolerated: an agent that never used Cortex simply scores zero.
func observeCases(casesDir string) trajectory.LauncherObservation {
	var observation trajectory.LauncherObservation
	observation.Evidence.Required = true
	observation.Disproof.Required = true
	if casesDir == "" {
		return observation
	}
	for _, dir := range caseDirectories(casesDir) {
		observeCase(dir, &observation)
	}
	return observation
}

// caseDirectories finds directories holding a case.json without following
// symlinks, so an agent cannot point the observer outside the case store.
func caseDirectories(root string) []string {
	var dirs []string
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if strings.Count(path, string(filepath.Separator))-rootDepth > maxObservedDepth {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Name() == caseFileName && entry.Type().IsRegular() {
			if len(dirs) < maxObservedCases {
				dirs = append(dirs, filepath.Dir(path))
			}
		}
		return nil
	})
	return dirs
}

func observeCase(dir string, observation *trajectory.LauncherObservation) {
	var snapshot struct {
		ChangeBoundary domain.ChangeBoundary `json:"changeBoundary"`
	}
	if readBoundedJSON(filepath.Join(dir, caseFileName), &snapshot) == nil && len(snapshot.ChangeBoundary.Files) > 0 {
		observation.BoundaryDeclared = true
	}

	evidence := &observation.Evidence
	_ = readLedger(filepath.Join(dir, evidenceFileName), func(line []byte) {
		var record domain.Evidence
		if json.Unmarshal(line, &record) != nil {
			return
		}
		evidence.Items++
		if sourcedEvidence(record) {
			evidence.Sourced++
		}
	})

	var hypotheses []domain.Hypothesis
	if readBoundedJSON(filepath.Join(dir, hypothesesFileName), &hypotheses) != nil {
		return
	}
	observeHypotheses(hypotheses, &observation.Disproof)
}

// sourcedEvidence requires both a named source tool and a locator, so an
// unlocated assertion does not count as verifiable provenance.
func sourcedEvidence(record domain.Evidence) bool {
	if record.Source.Tool == "" {
		return false
	}
	located := record.Location != nil && record.Location.File != ""
	return record.Source.URI != "" || located
}

func observeHypotheses(hypotheses []domain.Hypothesis, disproof *baseeval.DisproofObservation) {
	for _, hypothesis := range hypotheses {
		disproof.Hypotheses++
		if hypothesis.DisproveBy.Declared() {
			disproof.WithDisproofPath++
		}
		if hypothesis.Status == "" || hypothesis.Status == domain.HypActive {
			continue
		}
		disproof.Resolutions++
		if len(hypothesis.Supports) > 0 {
			disproof.EvidenceGroundedResolutions++
		}
	}
}

func readBoundedJSON(path string, into any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxCaseFileBytes {
		return os.ErrInvalid
	}
	file, err := os.Open(path) // #nosec G304 -- path is derived from the per-arm case store walk
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxCaseFileBytes+1))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

func readLedger(path string, fn func([]byte)) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return os.ErrInvalid
	}
	file, err := os.Open(path) // #nosec G304 -- path is derived from the per-arm case store walk
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(io.LimitReader(file, maxCaseFileBytes))
	scanner.Buffer(make([]byte, 0, 64<<10), maxLedgerLine)
	for scanner.Scan() {
		if line := scanner.Bytes(); len(line) > 0 {
			fn(line)
		}
	}
	return scanner.Err()
}
