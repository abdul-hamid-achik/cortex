/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import "testing"

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
