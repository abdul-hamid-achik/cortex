/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestVerificationClaimSpecs(t *testing.T) {
	claims, err := verificationClaimSpecs(
		[]string{"redirect works"}, []string{"redirect_works"}, []string{"code"}, []string{"codemap"}, []string{"codemap_review"},
	)
	if err != nil || len(claims) != 1 || claims[0].ID != "redirect_works" || claims[0].Contract != "codemap_review" {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if _, err := verificationClaimSpecs([]string{"a", "b"}, nil, []string{"code"}, nil, nil); err == nil {
		t.Fatal("misaligned claim surfaces must be rejected")
	}
	if _, err := verificationClaimSpecs([]string{"a"}, nil, []string{"code"}, nil, nil); err == nil {
		t.Fatal("typed claim without an exact contract must be rejected")
	}
	if _, err := verificationClaimSpecs([]string{"a", "b"}, []string{"one"}, []string{"code", "code"}, nil, []string{"x", "y"}); err == nil {
		t.Fatal("misaligned claim ids must be rejected")
	}
}

func TestParseClaimSpec(t *testing.T) {
	got, err := parseClaimSpec("id=redirect_works|surface=browser|verifier=cairntrace|contract=specs/checkout.yml|Login started at checkout returns to checkout")
	if err != nil {
		t.Fatalf("parseClaimSpec: %v", err)
	}
	want := domain.VerificationClaim{
		ID: "redirect_works", Surface: domain.SurfaceBrowser, Verifier: "cairntrace",
		Contract: "specs/checkout.yml", Statement: "Login started at checkout returns to checkout", Required: true,
	}
	if got != want {
		t.Fatalf("parseClaimSpec = %+v, want %+v", got, want)
	}

	// A statement may itself contain "=" and "|" — only recognized keys split.
	got, err = parseClaimSpec("surface=code|contract=codemap_review|returnTo=Login returns | keeps state")
	if err != nil {
		t.Fatalf("parseClaimSpec with special statement: %v", err)
	}
	if got.Statement != "returnTo=Login returns | keeps state" || got.Surface != domain.SurfaceCode {
		t.Fatalf("statement with = and | misparsed: %+v", got)
	}

	// Statement-only spec (no typed keys) is allowed.
	if got, _ := parseClaimSpec("the build passes"); got.Statement != "the build passes" || !got.Required {
		t.Fatalf("statement-only spec misparsed: %+v", got)
	}

	// No statement → error.
	if _, err := parseClaimSpec("id=x|surface=code"); err == nil {
		t.Fatal("a claim spec with no statement should error")
	}
}

func TestCLIVerifyClaimSpecEndToEnd(t *testing.T) {
	ws := cliRepo(t)
	id := startTask(t, ws)
	if _, err := runCLI(t, "-C", ws, "plan", id,
		"--hypothesis", "returnTo dropped :: review the diff", "--file", "f.go", "--uncertainty", "unsure"); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "f.go"), []byte("package a\nvar X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "--json", "-C", ws, "verify", id,
		"--claim-spec", "id=redirect_works|surface=code|verifier=codemap|contract=codemap_review|the redirect is preserved")
	if err != nil {
		t.Fatalf("verify --claim-spec: %v (%s)", err, out)
	}
	var env map[string]any
	if e := json.Unmarshal([]byte(out), &env); e != nil {
		t.Fatalf("verify output not JSON: %s", out)
	}
	if env["phase"] != "verifying" {
		t.Fatalf("verify --claim-spec should reach verifying, got: %v", env)
	}
}
