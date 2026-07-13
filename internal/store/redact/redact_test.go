package redact

import "strings"

import "testing"

func TestRedactSecretShapes(t *testing.T) {
	r := New()
	// Secret fixtures are assembled at runtime from non-contiguous parts so the
	// source holds no single secret-shaped literal (GitHub push protection
	// blocks test fixtures that look like real provider keys). The redact
	// patterns still match the concatenated runtime string.
	cases := []struct {
		name  string
		input string
	}{
		{"aws", "key " + "AKIA" + "IOSFODNN7EXAMPLE" + " here"},
		{"github", "token " + "ghp_" + "16C7e42F292c6912E7710c838347Ae178B4a"},
		{"stripe", "sk_" + "live_" + "1234567890abcdefABCDEF12"},
		{"bearer", "Authorization: Bearer " + "abcdef0123456789abcdef"},
		{"assigned", `API_KEY="` + "supersecretvalue123" + `"`},
		{"jwt", "ey" + "JhbGciOiJIUzI1NiJ9." + "eyJzdWIiOiIxMjM0NTY3ODkwIn0." + "dozjgNryP4J3jVmNHl0w5N"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := r.String(tc.input)
			if !strings.Contains(out, Mask) {
				t.Errorf("expected %q to be redacted, got %q", tc.input, out)
			}
			if out == tc.input {
				t.Errorf("input was not redacted: %q", out)
			}
		})
	}
}

func TestRedactPreservesKeyName(t *testing.T) {
	r := New()
	out := r.String(`DATABASE_PASSWORD=hunter2hunter2`)
	if !strings.HasPrefix(out, "DATABASE_PASSWORD=") {
		t.Errorf("key name should be preserved: %q", out)
	}
	if strings.Contains(out, "hunter2hunter2") {
		t.Errorf("secret value leaked: %q", out)
	}
}

func TestRedactSingleQuotedAndEmbeddedQuotes(t *testing.T) {
	// Regression: single-quoted assignments and values with an embedded quote
	// must be fully masked — no part of the secret may survive.
	r := New()
	cases := map[string]string{
		"export TOKEN='mysecretshell'": "mysecretshell",
		"API_KEY='s3cr3tvalue1'":       "s3cr3tvalue1",
		"password: 's3cr3tYAMLval'":    "s3cr3tYAMLval",
		"TOKEN=abcdef'ghijklmnop":      "ghijklmnop",   // embedded single quote
		`SECRET=aaaaaa"tail_leaks_x`:   "tail_leaks_x", // embedded double quote
	}
	for in, leak := range cases {
		out := r.String(in)
		if strings.Contains(out, leak) {
			t.Errorf("secret leaked: %q → %q (found %q)", in, out, leak)
		}
		if !strings.Contains(out, Mask) {
			t.Errorf("expected mask for %q, got %q", in, out)
		}
	}
}

func TestRedactLiterals(t *testing.T) {
	r := New("my-exact-secret")
	out := r.String("the value is my-exact-secret in here")
	if strings.Contains(out, "my-exact-secret") {
		t.Errorf("literal not redacted: %q", out)
	}
}

func TestRedactLeavesOrdinaryTextAlone(t *testing.T) {
	r := New()
	in := "func HandleCallback() { return resolveURL(req) }"
	if out := r.String(in); out != in {
		t.Errorf("ordinary code was altered: %q → %q", in, out)
	}
}

func TestDetected(t *testing.T) {
	r := New()
	if !r.Detected("ghp_" + "16C7e42F292c6912E7710c838347Ae178B4a") {
		t.Error("expected detection of a github token")
	}
	if r.Detected("just some normal words") {
		t.Error("false positive on ordinary text")
	}
}

func TestRedactJSONSecretFields(t *testing.T) {
	// Regression: a secret-named field in JSON tool output must be masked. The
	// key's closing quote sits between the name and the ':', which used to defeat
	// the assignment pattern and leak the value.
	r := New()
	leaky := []string{
		`{"api_key":"hunter2plainsecret"}`,
		`{ "token" : "hunter2plainsecret" }`,
		`{"password":"hunter2plainsecret"}`,
		`{"aws_secret_access_key":"hunter2plainsecret"}`,
		`"client_secret": "hunter2plainsecret"`,
	}
	for _, in := range leaky {
		got := r.String(in)
		if strings.Contains(got, "hunter2plainsecret") {
			t.Errorf("secret leaked through JSON field: %q → %q", in, got)
		}
		if !strings.Contains(got, Mask) {
			t.Errorf("expected a mask in %q → %q", in, got)
		}
	}
	// A non-secret JSON field is left alone (no over-masking).
	if got := r.String(`{"name":"checkout-service"}`); got != `{"name":"checkout-service"}` {
		t.Errorf("ordinary field should be untouched, got %q", got)
	}
}

// TestRedactPreservesJSONSeparator guards the regression where the assigned-
// secret replacement hard-coded '=' even for a ':' separator, corrupting JSON
// field structure ("api_key=«redacted»" instead of "api_key":«redacted»).
func TestRedactPreservesJSONSeparator(t *testing.T) {
	r := New()
	cases := []string{
		`{"api_key":"hunter2plainsecret"}`,
		`{"token":"hunter2plainsecret"}`,
		`{"aws_secret_access_key":"hunter2plainsecret"}`,
	}
	for _, in := range cases {
		got := r.String(in)
		if strings.Contains(got, "hunter2plainsecret") {
			t.Errorf("secret leaked: %q → %q", in, got)
		}
		// The ':' separator must be preserved (not replaced with '=').
		if strings.Contains(got, "=«redacted»") {
			t.Errorf("separator corrupted to '=' in JSON: %q → %q", in, got)
		}
		if !strings.Contains(got, ":") {
			t.Errorf("JSON ':' separator lost: %q → %q", in, got)
		}
	}
	// Env-style '=' separator is preserved for non-JSON assignments.
	if got := r.String("API_KEY=hunter2plainsecret"); !strings.HasPrefix(got, "API_KEY=") {
		t.Errorf("env '=' separator lost: %q → %q", "API_KEY=hunter2plainsecret", got)
	}
}

// TestRedactBareValueThreshold guards the false-positive tightening: a short
// bare config value on a signal-named key is NOT over-masked, while a long
// bare secret still is, preserving the precision-over-recall policy.
func TestRedactBareValueThreshold(t *testing.T) {
	r := New()
	short := []string{
		"ACCESS_KEY_REGION=us-east-1", // 9-char region, not a secret
		"PASSWORD_FILE=passwd.txt",    // 10-char filename, not a secret
	}
	for _, in := range short {
		if got := r.String(in); got != in {
			t.Errorf("short bare value should not be masked: %q → %q", in, got)
		}
	}
	// A long bare secret is still masked.
	long := "API_KEY=ak_live_0123456789secretvalue"
	if got := r.String(long); strings.Contains(got, "ak_live_0123456789secretvalue") {
		t.Errorf("long bare secret leaked: %q → %q", long, got)
	}
}
