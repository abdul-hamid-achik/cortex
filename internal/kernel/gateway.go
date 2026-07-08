package kernel

import (
	"context"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
)

// GatewaySelfCheck reports whether cortex itself is registered on the mcphub
// gateway that fronts it (and, when probe is set, whether a real MCP handshake
// succeeds). It is a read-only diagnostic: it stamps no evidence and touches no
// case store, so it lives outside the phase machine. The mcphub helper is
// intentionally not in the adapter Registry (it answers operational meta-info,
// not task evidence), so the kernel constructs it here to keep cmd thin.
func (k *Kernel) GatewaySelfCheck(ctx context.Context, serverName string, probe bool) adapters.GatewayReport {
	if serverName == "" {
		serverName = adapters.DefaultServerName
	}
	return adapters.NewMcphub().GatewaySelfCheck(ctx, serverName, probe)
}
