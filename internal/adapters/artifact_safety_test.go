package adapters

import (
	"strings"
	"testing"
)

func TestValidateArtifactIDKeepsCaseRawGrammarStrict(t *testing.T) {
	for _, id := range []string{"raw_1", "raw-token", "A9"} {
		if err := ValidateArtifactID(id); err != nil {
			t.Errorf("strict artifact id %q was rejected: %v", id, err)
		}
	}
	if err := ValidateArtifactID("raw.1"); err == nil {
		t.Fatal("case raw ids must reject dots to prevent filename aliases")
	}
}

func TestValidateFcheapStashIDAcceptsOpaqueV029IDs(t *testing.T) {
	for _, id := range []string{
		"legacy_stash_20260712_192141",
		"runbundle_20260712_192141.516352000_1bdc40f3792a51947518faf8",
		"stash.with-dots_and-dashes",
		strings.Repeat("a", MaxArtifactPreviewIDBytes),
	} {
		if err := ValidateFcheapStashID(id); err != nil {
			t.Errorf("fcheap stash id %q was rejected: %v", id, err)
		}
	}
}

func TestValidateFcheapStashIDRejectsUnsafeTokens(t *testing.T) {
	for _, id := range []string{
		"", ".", "..", ".hidden", "-flag", "../escape", `safe\escape`,
		"safe/escape", "safe%2fescape", "safe?query", "safe#fragment",
		"safe id", "safe\tid", "stash_é", strings.Repeat("a", MaxArtifactPreviewIDBytes+1),
	} {
		if err := ValidateFcheapStashID(id); err == nil {
			t.Errorf("unsafe fcheap stash id %q was accepted", id)
		}
	}
}
