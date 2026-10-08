package csvout

import "testing"

func TestFieldPlainValuesUnchanged(t *testing.T) {
	for _, in := range []string{"", "plain", "two words", "42"} {
		if got := Field(in); got != in {
			t.Errorf("Field(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestFieldQuotesCommas(t *testing.T) {
	if got, want := Field("a,b"), `"a,b"`; got != want {
		t.Fatalf("Field = %q, want %q", got, want)
	}
}

func TestFieldEscapesQuotes(t *testing.T) {
	if got, want := Field(`say "hi"`), `"say \"hi\""`; got != want {
		t.Fatalf("Field = %q, want %q", got, want)
	}
}
