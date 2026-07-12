/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

func TestCLIReadArtifactEnforcesOwnerAndBinaryOptIn(t *testing.T) {
	ws := cliRepo(t)
	owner := startTask(t, ws)
	other := startTask(t, ws)
	k, err := kernel.New(config.For(ws))
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Store().WriteRaw(owner, "raw_binary", string([]byte{0xff, 0x00, 0x80, 0x01})); err != nil {
		t.Fatal(err)
	}
	ref := "case://" + owner + "/raw/raw_binary"
	if _, err := runCLI(t, "--json", "-C", ws, "read-artifact", other, ref); err == nil || !strings.Contains(err.Error(), "must belong") {
		t.Fatalf("cross-task CLI read must fail ownership, got %v", err)
	}
	if _, err := runCLI(t, "--json", "-C", ws, "read-artifact", owner, ref); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary CLI read without opt-in must fail, got %v", err)
	}
	out, err := runCLI(t, "--json", "-C", ws, "read-artifact", owner, ref,
		"--max-bytes", "2", "--allow-binary")
	if err != nil {
		t.Fatalf("binary CLI opt-in: %v (%s)", err, out)
	}
	var preview kernel.ArtifactPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatalf("binary CLI output is not JSON: %v (%s)", err, out)
	}
	decoded, err := base64.StdEncoding.DecodeString(preview.Content)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Encoding != "base64" || !preview.Sensitive || len(decoded) != 2 || !preview.Truncated {
		t.Fatalf("binary CLI preview = %+v decoded=%v", preview, decoded)
	}
}
