/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import "testing"

func TestVerificationClaimSpecs(t *testing.T) {
	claims, err := verificationClaimSpecs(
		[]string{"redirect works"}, []string{"code"}, []string{"codemap"}, []string{"codemap_review"},
	)
	if err != nil || len(claims) != 1 || claims[0].Contract != "codemap_review" {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if _, err := verificationClaimSpecs([]string{"a", "b"}, []string{"code"}, nil, nil); err == nil {
		t.Fatal("misaligned claim surfaces must be rejected")
	}
	if _, err := verificationClaimSpecs([]string{"a"}, []string{"code"}, nil, nil); err == nil {
		t.Fatal("typed claim without an exact contract must be rejected")
	}
}
