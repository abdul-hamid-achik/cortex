package adapters

import (
	"context"
	"time"
)

// Mcphub is a lightweight helper for cortex's OWN gateway registration, not a
// task-evidence adapter. mcphub fronts cortex as a downstream MCP server, so the
// seam is inverted: cortex asks mcphub "am I registered, and has the gateway
// ever routed a call to me?". It is deliberately NOT added to the kernel's
// adapter Registry — it must not pollute the Health() fan-out or the
// investigate/verify evidence loop with operational meta-info.
type Mcphub struct{ tool }

// NewMcphub builds the mcphub gateway helper. The 60s budget covers `--probe`,
// which spawns the downstream server for a real handshake.
func NewMcphub() *Mcphub { return &Mcphub{tool: newTool("mcphub", 60*time.Second)} }

// GatewayReport mirrors mcphub's scopedServerReport (`mcphub doctor --server
// <name> --json`) plus two cortex-added fields. The probe-only fields are
// pointers so "not probed" is distinguishable from "probed and false".
type GatewayReport struct {
	Server       string         `json:"server"`
	Registered   bool           `json:"registered"`
	Enabled      bool           `json:"enabled"`
	OnPath       bool           `json:"on_path"`
	Remote       bool           `json:"remote"`
	HandshakeOK  *bool          `json:"handshake_ok"`
	ToolCount    *int           `json:"tool_count"`
	ProbeError   string         `json:"probe_error"`
	Agents       []GatewayAgent `json:"agents"`
	ProxiedCalls int64          `json:"proxied_calls"`
	Unused       bool           `json:"unused"`

	// Supported reports whether the self-check itself could run (mcphub present
	// and speaks --server). Detail explains a non-supported result. These are NOT
	// from mcphub — they let cortex distinguish "unsupported/unreachable" from a
	// genuine "registered:false", so an old/absent mcphub never raises a false
	// "you are not wired in" alarm.
	Supported bool   `json:"supported"`
	Detail    string `json:"detail,omitempty"`
}

// GatewayAgent is one harness's view of the server through the gateway.
type GatewayAgent struct {
	Agent string `json:"agent"`
	Mode  string `json:"mode"`
	State string `json:"state"`
}

// GatewaySelfCheck asks mcphub whether serverName is registered on the gateway
// (and, when probe is set, completes a real handshake reporting the tool count).
// It never fabricates: a missing/old mcphub yields Supported=false with a Detail,
// never a misleading registered=false.
func (m *Mcphub) GatewaySelfCheck(ctx context.Context, serverName string, probe bool) GatewayReport {
	if serverName == "" {
		serverName = "cortex"
	}
	if !binExists(m.bin) {
		return GatewayReport{Server: serverName, Detail: "mcphub not on PATH — cannot verify gateway registration"}
	}
	args := []string{"doctor", "--server", serverName, "--json"}
	if probe {
		args = append(args, "--probe")
	}
	stdout, stderr, _, err := m.exec(ctx, "", args...)
	if err != nil {
		return GatewayReport{Server: serverName, Detail: "mcphub gateway self-check unavailable: " + err.Error()}
	}
	var rep GatewayReport
	if derr := decodeJSON(stdout, &rep); derr != nil {
		// An old mcphub without --server prints "unknown flag" to stderr; a missing
		// config prints "no config…". Both surface as unsupported, never as
		// registered=false.
		detail := firstNonEmpty(firstLine(stderr), firstLine(stdout), "mcphub returned no gateway report")
		return GatewayReport{Server: serverName, Detail: "gateway self-check unsupported (update mcphub): " + clip(detail, 140)}
	}
	rep.Supported = true
	if rep.Server == "" {
		rep.Server = serverName
	}
	return rep
}
