/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import "testing"

func TestParseDecisionOptions(t *testing.T) {
	options, err := parseDecisionOptions([]string{"safe=Use safe path|Slower", "fast=Use fast path|Higher risk"})
	if err != nil || len(options) != 2 || options[0].ID != "safe" || options[0].Consequence != "Slower" {
		t.Fatalf("options=%+v err=%v", options, err)
	}
	if _, err := parseDecisionOptions([]string{"missing separator"}); err == nil {
		t.Fatal("malformed option accepted")
	}
}
