package adapters

import (
	"context"
	"testing"
)

func TestMcphubGatewayRegisteredAndProbed(t *testing.T) {
	// Real live shape of `mcphub doctor --server cortex --probe --json`.
	fixture := `{"server":"cortex","registered":true,"enabled":true,"on_path":true,
	  "handshake_ok":true,"tool_count":10,
	  "agents":[{"agent":"claude","mode":"gateway","state":"in sync"},{"agent":"opencode","mode":"direct","state":"in sync"}],
	  "proxied_calls":0,"unused":true}`
	m := &Mcphub{tool: fakeTool(fixture, "", 0)}
	rep := m.GatewaySelfCheck(context.Background(), "cortex", true)
	if !rep.Supported || !rep.Registered || rep.ToolCount == nil || *rep.ToolCount != 10 {
		t.Fatalf("expected supported+registered+10 tools, got %+v", rep)
	}
	if rep.HandshakeOK == nil || !*rep.HandshakeOK || len(rep.Agents) != 2 {
		t.Errorf("probe fields not parsed: %+v", rep)
	}
	if !rep.Unused || rep.ProxiedCalls != 0 {
		t.Errorf("expected the 'never routed' signal (unused + 0 calls), got unused=%v calls=%d", rep.Unused, rep.ProxiedCalls)
	}
}

func TestMcphubGatewayUnregistered(t *testing.T) {
	// A bogus/unregistered server: registered=false, no probe fields.
	fixture := `{"server":"nope","registered":false,"enabled":false,"on_path":false,"agents":[],"proxied_calls":0}`
	m := &Mcphub{tool: fakeTool(fixture, "", 0)}
	rep := m.GatewaySelfCheck(context.Background(), "nope", false)
	if !rep.Supported || rep.Registered {
		t.Errorf("expected supported but not registered, got %+v", rep)
	}
	if rep.HandshakeOK != nil {
		t.Errorf("an unprobed report must leave handshake_ok nil, got %v", *rep.HandshakeOK)
	}
}

func TestMcphubOldBinaryIsUnsupportedNotUnregistered(t *testing.T) {
	// An OLD mcphub without --server prints "unknown flag" to stderr, empty
	// stdout, non-zero exit (exit is data → err nil). It must be reported as
	// UNSUPPORTED, never as a false "you are not registered".
	m := &Mcphub{tool: fakeTool("", "Error: unknown flag: --server\n", 1)}
	rep := m.GatewaySelfCheck(context.Background(), "cortex", false)
	if rep.Supported {
		t.Error("an old mcphub that can't self-check must be Supported=false")
	}
	if rep.Registered {
		t.Error("must NOT report registered=false when the check simply couldn't run")
	}
	if rep.Detail == "" {
		t.Error("an unsupported result should explain why")
	}
}
