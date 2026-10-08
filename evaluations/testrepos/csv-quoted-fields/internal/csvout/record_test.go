package csvout

import "testing"

func TestRecordJoinsFields(t *testing.T) {
	got := Record([]string{"id", "a,b", "plain"})
	if want := `id,"a,b",plain`; got != want {
		t.Fatalf("Record = %q, want %q", got, want)
	}
}

func TestRecordEmpty(t *testing.T) {
	if got := Record(nil); got != "" {
		t.Fatalf("Record(nil) = %q, want empty", got)
	}
}
