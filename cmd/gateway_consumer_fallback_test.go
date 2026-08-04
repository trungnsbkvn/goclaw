package cmd

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
)

// A per-thread agent override that names a missing/disabled agent must
// degrade the thread to the instance default carried in metadata — never to
// the error-reply path, which on an external customer channel is silence.

func fallbackTestDeps(t *testing.T, registered ...string) *ConsumerDeps {
	t.Helper()
	router := agent.NewRouter()
	for _, id := range registered {
		router.Register(deliveryRuntimeTestAgent{id: id})
	}
	return &ConsumerDeps{Agents: router}
}

func TestGetAgentLoopWithFallbackResolvesDirectly(t *testing.T) {
	deps := fallbackTestDeps(t, "pws")
	msg := bus.InboundMessage{Metadata: map[string]string{channels.MetaAgentIDFallback: "sales-agent"}}
	loop, id, err := getAgentLoopWithFallback(context.Background(), deps, msg, "pws")
	if err != nil || loop == nil {
		t.Fatalf("err = %v, loop = %v", err, loop)
	}
	if id != "pws" {
		t.Fatalf("agentID = %q, want the override target when it resolves", id)
	}
}

func TestGetAgentLoopWithFallbackDegradesToDefault(t *testing.T) {
	deps := fallbackTestDeps(t, "sales-agent")
	msg := bus.InboundMessage{Metadata: map[string]string{channels.MetaAgentIDFallback: "sales-agent"}}
	loop, id, err := getAgentLoopWithFallback(context.Background(), deps, msg, "gone-agent")
	if err != nil || loop == nil {
		t.Fatalf("expected fallback to resolve, got err = %v", err)
	}
	if id != "sales-agent" {
		t.Fatalf("agentID = %q, want the instance default — session keys must be built from it", id)
	}
}

func TestGetAgentLoopWithFallbackNoFallbackKeepsErrorPath(t *testing.T) {
	deps := fallbackTestDeps(t)
	msg := bus.InboundMessage{}
	loop, id, err := getAgentLoopWithFallback(context.Background(), deps, msg, "gone-agent")
	if err == nil || loop != nil {
		t.Fatal("without fallback metadata the existing error path must be preserved")
	}
	if id != "gone-agent" {
		t.Fatalf("agentID = %q, want the requested agent for the error log", id)
	}
}

func TestGetAgentLoopWithFallbackBothMissingReturnsOriginalError(t *testing.T) {
	deps := fallbackTestDeps(t)
	msg := bus.InboundMessage{Metadata: map[string]string{channels.MetaAgentIDFallback: "also-gone"}}
	loop, _, err := getAgentLoopWithFallback(context.Background(), deps, msg, "gone-agent")
	if err == nil || loop != nil {
		t.Fatal("both agents missing must surface the original error")
	}
}
