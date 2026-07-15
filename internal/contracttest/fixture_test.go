package contracttest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeRemovesNondeterministicAndPrivateValues(t *testing.T) {
	payload := map[string]any{
		"taskId": "task_06ABC123", "workspace": "/private/tmp/case-123",
		"at": "2026-07-14T12:30:00.123Z", "receipt": "vr_06DEF456",
		"windows":     `C:\Users\alice\AppData\Local\Temp\case-123`,
		"windowsTemp": `C:\Windows\Temp\case-123`,
		"envTemp":     `%TEMP%\case-123`,
		"unc":         `\\fileserver\private\alice\case-123`,
	}
	got, err := Normalize(payload, map[string]string{"/private/tmp/case-123": "$WORKSPACE"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, forbidden := range []string{
		"06ABC123", "06DEF456", "/private/tmp", "2026-07-14",
		`C:\\Users\\alice`, `\\\\fileserver\\private`,
		`C:\\Windows\\Temp`, `%TEMP%`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("normalized fixture leaked %q: %s", forbidden, text)
		}
	}
	for _, marker := range []string{"$WINDOWS_PRIVATE_PATH", "$WINDOWS_TEMP_PATH", "$UNC_PRIVATE_PATH"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("normalized fixture omitted literal marker %q: %s", marker, text)
		}
	}
}

func TestDecodeRejectsUnknownFutureVersion(t *testing.T) {
	data, err := json.Marshal(Fixture{
		ContractVersion: Version + 1, ID: "future", GeneratedBy: "test",
		Classification: "illustrative", SensitiveDataPolicy: sensitiveDataPolicy,
		SizeBehavior: "bounded", Payload: json.RawMessage(`{"ok":false}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "unsupported contract fixture version") {
		t.Fatalf("future fixture version was not rejected: %v", err)
	}
}

func TestDecodeRejectsUnknownWrapperFields(t *testing.T) {
	fixture, err := NewFixture("strict", "test", "illustrative", "bounded", map[string]bool{"ok": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "\n}", ",\n  \"future\": true\n}", 1))
	if _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown wrapper field was accepted: %v", err)
	}
}
