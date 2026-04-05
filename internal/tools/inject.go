// Package tools builds the tool-calling protocol injected into the Claude
// system prompt and parses the XML tool_call blocks Claude emits in response.
package tools

import (
	"strings"

	"github.com/s1hon/claude-proxy/internal/openai"
)

// blockedTools are gateway-internal tool names that must never be surfaced to
// the model as available tools.
var blockedTools = map[string]struct{}{
	"sessions_send":  {},
	"sessions_spawn": {},
	"gateway":        {},
}

// BuildInstructions returns the tool-calling protocol text to append to the
// system prompt. Returns "" if no tools are supplied.
func BuildInstructions(tools []openai.Tool) string {
	if len(tools) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n---\n\n")
	b.WriteString("## Tool Calling Protocol\n\n")
	b.WriteString("When you need to use a tool, output EXACTLY this format and then STOP:\n\n")
	b.WriteString("<tool_call>\n")
	b.WriteString(`{"name": "tool_name", "arguments": {"key": "value"}}` + "\n")
	b.WriteString("</tool_call>\n\n")
	b.WriteString("You may request multiple tools at once:\n\n")
	b.WriteString("<tool_call>\n")
	b.WriteString(`{"name": "web_search", "arguments": {"query": "bitcoin price"}}` + "\n")
	b.WriteString("</tool_call>\n")
	b.WriteString("<tool_call>\n")
	b.WriteString(`{"name": "memory_search", "arguments": {"query": "user preferences"}}` + "\n")
	b.WriteString("</tool_call>\n\n")
	b.WriteString("CRITICAL RULES:\n")
	b.WriteString("- Do NOT execute tools yourself. Do NOT use Bash, Read, Write, Edit, WebSearch, WebFetch, Glob, Grep, or any native tools.\n")
	b.WriteString("- Output <tool_call> blocks and STOP. The orchestrator will execute them and provide results.\n")
	b.WriteString("- If you do not need any tools, just respond with your answer directly.\n")
	b.WriteString("- The conversation may already contain tool results from previous turns — use them, do not re-request.\n\n")
	b.WriteString("Available tools:\n")

	for _, t := range tools {
		name := t.Function.Name
		if name == "" {
			continue
		}
		if _, blocked := blockedTools[name]; blocked {
			continue
		}
		desc := t.Function.Description
		b.WriteString("- **")
		b.WriteString(name)
		b.WriteString("**: ")
		b.WriteString(desc)
		b.WriteString("\n")
	}

	return b.String()
}
