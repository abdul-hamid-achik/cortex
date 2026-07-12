package mcp

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

func TestMCPReadArtifactEnforcesOwnerAndBinaryOptIn(t *testing.T) {
	cs, ws := connectProfile(t, ProfileAgent)
	owner := callEnvelope(t, cs, "cortex_start_task", map[string]any{"goal": "own binary", "workspace": ws})["taskId"].(string)
	other := callEnvelope(t, cs, "cortex_start_task", map[string]any{"goal": "other task", "workspace": ws})["taskId"].(string)
	k, err := kernel.New(config.For(ws))
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Store().WriteRaw(owner, "raw_binary", string([]byte{0xff, 0x00, 0x80, 0x01})); err != nil {
		t.Fatal(err)
	}
	ref := "case://" + owner + "/raw/raw_binary"

	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "other task", args: map[string]any{"taskId": other, "workspace": ws, "ref": ref}, want: "must belong"},
		{name: "binary default", args: map[string]any{"taskId": owner, "workspace": ws, "ref": ref}, want: "binary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "cortex_read_artifact", Arguments: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError || !strings.Contains(textOf(res), tc.want) {
				t.Fatalf("read-artifact should fail with %q: error=%t text=%s", tc.want, res.IsError, textOf(res))
			}
		})
	}

	preview := callEnvelope(t, cs, "cortex_read_artifact", map[string]any{
		"taskId": owner, "workspace": ws, "ref": ref, "maxBytes": 2, "allowBinary": true,
	})
	decoded, err := base64.StdEncoding.DecodeString(preview["content"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if preview["encoding"] != "base64" || preview["sensitive"] != true || len(decoded) != 2 || preview["truncated"] != true {
		t.Fatalf("binary MCP preview = %v decoded=%v", preview, decoded)
	}
}
