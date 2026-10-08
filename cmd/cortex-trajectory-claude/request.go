/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/eval/trajectory"
)

const maxRequestBytes = 1 << 20

// readRequest reads the single bounded request and returns the raw bytes.
func readRequest(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxRequestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read request: %w", err)
	}
	if len(data) > maxRequestBytes {
		return nil, fmt.Errorf("request exceeds %d bytes", maxRequestBytes)
	}
	return data, nil
}

// requestPayload is the raw stdin with exactly one trailing newline removed.
// The runner hashes the marshalled request before appending that newline, so
// the digest must be taken over these exact bytes and never over a re-marshal.
func requestPayload(raw []byte) []byte {
	return bytes.TrimSuffix(raw, []byte("\n"))
}

func requestDigest(raw []byte) string {
	sum := sha256.Sum256(requestPayload(raw))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// decodeRequest strictly decodes one request object: unknown fields and
// trailing data are rejected.
func decodeRequest(raw []byte) (trajectory.LauncherRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(requestPayload(raw)))
	decoder.DisallowUnknownFields()
	var request trajectory.LauncherRequest
	if err := decoder.Decode(&request); err != nil {
		return trajectory.LauncherRequest{}, fmt.Errorf("decode launcher request: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return trajectory.LauncherRequest{}, errors.New("launcher request contains trailing data")
	}
	return request, nil
}

// validateRequest checks the fields this launcher depends on before anything
// is executed.
func validateRequest(request trajectory.LauncherRequest) error {
	if request.SchemaVersion != trajectory.ProtocolSchemaVersion {
		return fmt.Errorf("unsupported launcher request schema version %d", request.SchemaVersion)
	}
	if !filepath.IsAbs(request.Workspace) {
		return errors.New("request workspace must be an absolute path")
	}
	if info, err := os.Stat(request.Workspace); err != nil || !info.IsDir() {
		return errors.New("request workspace is not an accessible directory")
	}
	if strings.TrimSpace(request.Goal) == "" {
		return errors.New("request goal is empty")
	}
	identifier := request.Model.Identifier
	if strings.TrimSpace(identifier) == "" || strings.HasPrefix(identifier, "-") || strings.ContainsAny(identifier, "\x00\r\n") {
		return errors.New("request model identifier is empty or unsafe")
	}
	if request.Budget.MaxToolCalls < 1 {
		return errors.New("request tool-call budget must be positive")
	}
	if request.Budget.MaxWallTime.Value() <= 0 {
		return errors.New("request wall-time budget must be positive")
	}
	if request.Budget.MaxEstimatedCostMicros < 0 {
		return errors.New("request cost budget cannot be negative")
	}
	return nil
}

// modelPinsUnsupportedControls refuses manifests that pin sampling controls
// the Claude CLI cannot set. Reporting a pinned temperature or seed as effective
// would be a false provenance claim.
func modelPinsUnsupportedControls(model trajectory.Model) error {
	if model.Temperature != nil {
		return errors.New("the claude cli cannot set temperature; the manifest must use temperature_unsupported_reason")
	}
	if model.Seed != nil {
		return errors.New("the claude cli cannot set a seed; the manifest must use seed_unsupported_reason")
	}
	if strings.TrimSpace(model.TemperatureUnsupportedReason) == "" || strings.TrimSpace(model.SeedUnsupportedReason) == "" {
		return errors.New("the manifest must state why temperature and seed are unsupported")
	}
	return nil
}
