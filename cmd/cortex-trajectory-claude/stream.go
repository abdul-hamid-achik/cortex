/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"

	baseeval "github.com/abdul-hamid-achik/cortex/internal/eval"
)

// maxStreamLine bounds one stream-json line. Longer lines are discarded rather
// than buffered without limit.
const maxStreamLine = 8 << 20

type streamContentBlock struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// streamMCPServer is one MCP server status reported in the init event.
type streamMCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type streamUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

type streamEvent struct {
	Type       string            `json:"type"`
	Subtype    string            `json:"subtype"`
	Model      string            `json:"model"`
	MCPServers []streamMCPServer `json:"mcp_servers"`
	Message    *struct {
		Content []streamContentBlock `json:"content"`
	} `json:"message"`
	IsError      bool         `json:"is_error"`
	Result       string       `json:"result"`
	TotalCostUSD *float64     `json:"total_cost_usd"`
	Usage        *streamUsage `json:"usage"`
}

// resultEvent is the terminal `result` event of a stream-json run.
type resultEvent struct {
	Subtype string
	IsError bool
	Text    string
	// CostMicros and the token counts are nil when the event did not carry them.
	CostMicros   *int64
	InputTokens  *int64
	OutputTokens *int64
}

// streamParser consumes the agent's stdout incrementally. It implements
// io.Writer so exec copies the pipe into it, and it can stop the agent the
// moment the tool-call budget would be exceeded.
type streamParser struct {
	mu       sync.Mutex
	buffer   []byte
	overflow bool
	maxCalls int
	onCap    func()

	seenTools map[string]bool
	toolCalls int
	capped    bool

	sawInit    bool
	initModel  string
	mcpServers []streamMCPServer
	toolNames  map[string]int
	result     *resultEvent
	malformed  int
	oversized  int
}

func newStreamParser(maxCalls int, onCap func()) *streamParser {
	return &streamParser{maxCalls: maxCalls, onCap: onCap, seenTools: map[string]bool{}, toolNames: map[string]int{}}
}

func (p *streamParser) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rest := data
	for len(rest) > 0 {
		newline := bytes.IndexByte(rest, '\n')
		if newline < 0 {
			p.appendPartial(rest)
			break
		}
		p.appendPartial(rest[:newline])
		p.finishLine()
		rest = rest[newline+1:]
	}
	return len(data), nil
}

func (p *streamParser) appendPartial(chunk []byte) {
	if p.overflow {
		return
	}
	if len(p.buffer)+len(chunk) > maxStreamLine {
		p.overflow = true
		p.buffer = nil
		return
	}
	p.buffer = append(p.buffer, chunk...)
}

func (p *streamParser) finishLine() {
	line, overflow := p.buffer, p.overflow
	p.buffer, p.overflow = nil, false
	if overflow {
		p.oversized++
		return
	}
	p.handleLine(line)
}

// Flush handles a final line that had no trailing newline.
func (p *streamParser) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.buffer) > 0 || p.overflow {
		p.finishLine()
	}
}

func (p *streamParser) handleLine(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var event streamEvent
	if err := json.Unmarshal(line, &event); err != nil {
		p.malformed++
		return
	}
	switch event.Type {
	case "system":
		if event.Subtype == "init" && !p.sawInit {
			p.sawInit = true
			p.initModel = event.Model
			p.mcpServers = append([]streamMCPServer(nil), event.MCPServers...)
		}
	case "assistant":
		p.countToolUses(event)
	case "result":
		if p.result == nil {
			p.result = newResultEvent(event)
		}
	}
}

// countToolUses counts distinct tool_use blocks. The stream may repeat a block
// across partial assistant events, so blocks are deduplicated by id.
func (p *streamParser) countToolUses(event streamEvent) {
	if p.capped || event.Message == nil {
		return
	}
	for _, block := range event.Message.Content {
		if block.Type != "tool_use" {
			continue
		}
		if block.ID != "" {
			if p.seenTools[block.ID] {
				continue
			}
			p.seenTools[block.ID] = true
		}
		if p.toolCalls+1 > p.maxCalls {
			p.capped = true
			if p.onCap != nil {
				p.onCap()
			}
			return
		}
		p.toolCalls++
		p.toolNames[block.Name]++
	}
}

func newResultEvent(event streamEvent) *resultEvent {
	result := &resultEvent{Subtype: event.Subtype, IsError: event.IsError, Text: event.Result}
	if cost := event.TotalCostUSD; cost != nil && !math.IsNaN(*cost) && !math.IsInf(*cost, 0) && *cost >= 0 {
		micros := int64(math.Round(*cost * 1e6))
		result.CostMicros = &micros
	}
	if usage := event.Usage; usage != nil {
		input := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
		output := usage.OutputTokens
		if input >= 0 && output >= 0 {
			result.InputTokens, result.OutputTokens = &input, &output
		}
	}
	return result
}

// snapshot returns a consistent copy of the parsed state.
func (p *streamParser) snapshot() streamState {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := streamState{
		ToolCalls: p.toolCalls, Capped: p.capped, SawInit: p.sawInit, InitModel: p.initModel,
		Malformed: p.malformed, Oversized: p.oversized,
	}
	state.MCPServers = append([]streamMCPServer(nil), p.mcpServers...)
	state.ToolNames = make(map[string]int, len(p.toolNames))
	for name, count := range p.toolNames {
		state.ToolNames[name] = count
	}
	if p.result != nil {
		copied := *p.result
		state.Result = &copied
	}
	return state
}

type streamState struct {
	ToolCalls int
	Capped    bool
	SawInit   bool
	InitModel string
	Result    *resultEvent
	Malformed int
	Oversized int
	// MCPServers and ToolNames are diagnostic instrumentation: whether the
	// cortex server connected and which tools the agent actually used.
	MCPServers []streamMCPServer
	ToolNames  map[string]int
}

var completionLine = regexp.MustCompile(`^COMPLETION:\s*(verified|unverified|failed|incomplete)\s*$`)

// parseCompletion reads the final non-empty line of the agent's message. A
// missing or malformed line is reported as incomplete, never guessed.
func parseCompletion(text string) baseeval.CompletionLabel {
	lines := strings.Split(strings.TrimRight(text, " \t\r\n"), "\n")
	last := strings.TrimRight(lines[len(lines)-1], " \t\r")
	if match := completionLine.FindStringSubmatch(last); match != nil {
		return baseeval.CompletionLabel(match[1])
	}
	return baseeval.CompletionIncomplete
}

// logStreamUsage reports MCP server status and per-tool call counts to
// stderr so an operator can tell "the agent ignored Cortex" from "the Cortex
// server never connected". It never echoes tool inputs or model text.
func logStreamUsage(state streamState, logf func(string, ...any)) {
	for _, server := range state.MCPServers {
		logf("mcp server %s: %s", server.Name, server.Status)
	}
	names := make([]string, 0, len(state.ToolNames))
	for name := range state.ToolNames {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", name, state.ToolNames[name]))
	}
	if len(parts) > 0 {
		logf("tool calls by name: %s", strings.Join(parts, ", "))
	}
}
