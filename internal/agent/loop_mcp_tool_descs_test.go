package agent

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// Regression: the actor's per-user MCP tools must reach the system prompt.
//
// Live incident 2026-07-28 (prod tenant yp). buildMCPToolDescs only walked
// toolNames, which comes from the SHARED registry — and per-user MCP tools are
// deliberately never registered there (cross-user identity leak, see
// getUserMCPTools). So the loop could never reach them: the provider request
// carried 34 per-user tool definitions while "## Tooling" in the prompt listed
// 5 core tools and the MCP description section was omitted entirely
// (promptLen identical turn after turn while 34 tools were loaded).
//
// deepseek-chat then answered from the prompt — "I don't have the permissions
// for that" — while intermittently calling a tool it could still see in the
// schema. The tools were wired correctly the whole time; only the prompt
// disagreed.
func TestBuildMCPToolDescs_IncludesActorPerUserTools(t *testing.T) {
	const actor = "1907496579"
	l := &Loop{tools: tools.NewRegistry()}
	l.mcpUserTools.Store(actor, []tools.Tool{
		&mockTool{name: "mcp_jushub_list_my_deadlines", desc: "List your upcoming deadlines"},
		&mockTool{name: "mcp_jushub_ask_assistant", desc: "Ask the JusHub assistant"},
	})

	// toolNames holds only shared-registry names — exactly the production shape.
	descs := l.buildMCPToolDescs([]string{"session_status", "send_message"}, actor)

	if len(descs) != 2 {
		t.Fatalf("got %d MCP descriptions, want 2 — the actor's per-user tools are "+
			"missing from the prompt while the LLM receives their definitions", len(descs))
	}
	if descs["mcp_jushub_list_my_deadlines"] != "List your upcoming deadlines" {
		t.Errorf("description not carried through: %q", descs["mcp_jushub_list_my_deadlines"])
	}

	// A different actor must still see nothing — ownership of the cache entry is
	// the authorization boundary, and executeToolForActor resolves from the same
	// per-actor map.
	if other := l.buildMCPToolDescs([]string{"session_status"}, "6498804981189054362"); other != nil {
		t.Errorf("leaked %d tool descriptions to another actor", len(other))
	}
	if anon := l.buildMCPToolDescs([]string{"session_status"}, ""); anon != nil {
		t.Errorf("leaked %d tool descriptions to an empty actor id", len(anon))
	}
}
