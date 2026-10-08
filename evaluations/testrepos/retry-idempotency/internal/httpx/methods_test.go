package httpx

import "testing"

func TestIsSafe(t *testing.T) {
	for method, want := range map[string]bool{
		"GET": true, "HEAD": true, "OPTIONS": true, "TRACE": true,
		"POST": false, "PUT": false, "DELETE": false, "PATCH": false,
	} {
		if got := IsSafe(method); got != want {
			t.Errorf("IsSafe(%q) = %v, want %v", method, got, want)
		}
	}
}
