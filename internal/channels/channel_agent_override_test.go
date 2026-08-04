package channels

import (
	"context"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
)

// One Zalo account, several personas: the override map routes listed threads
// (staff DMs, internal groups) to a different agent than the instance default,
// while every unlisted thread — i.e. every stranger — keeps the default. These
// tests pin the funnel behavior: who gets overridden, and that the instance
// default always rides along in metadata so the consumer can degrade to it.

func overrideTestChannel(t *testing.T, overrides map[string]string) (*BaseChannel, *bus.MessageBus) {
	t.Helper()
	mb := bus.New()
	c := NewBaseChannel("zalo-sales", mb, nil)
	c.SetAgentID("sales-agent")
	c.SetAgentOverrides(overrides)
	return c, mb
}

func consumeOne(t *testing.T, mb *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	msg, ok := mb.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no inbound message published")
	}
	return msg
}

func TestAgentOverrideDMStrangerKeepsDefault(t *testing.T) {
	c, mb := overrideTestChannel(t, map[string]string{"user:staff-1": "pws"})
	c.HandleAuthorizedMessage("stranger-9", "stranger-9", "hello", nil, nil, "direct")
	msg := consumeOne(t, mb)
	if msg.AgentID != "sales-agent" {
		t.Fatalf("stranger DM AgentID = %q, want the instance default", msg.AgentID)
	}
	if msg.Metadata[MetaAgentIDFallback] != "" {
		t.Fatalf("no override fired, but fallback metadata = %q", msg.Metadata[MetaAgentIDFallback])
	}
}

func TestAgentOverrideDMListedStaffRoutesToOverride(t *testing.T) {
	c, mb := overrideTestChannel(t, map[string]string{"user:staff-1": "pws"})
	c.HandleAuthorizedMessage("staff-1", "staff-1", "hi", nil, nil, "direct")
	msg := consumeOne(t, mb)
	if msg.AgentID != "pws" {
		t.Fatalf("staff DM AgentID = %q, want override target", msg.AgentID)
	}
	if got := msg.Metadata[MetaAgentIDFallback]; got != "sales-agent" {
		t.Fatalf("fallback metadata = %q, want the instance default", got)
	}
}

func TestAgentOverrideGroupListedRoutesToOverride(t *testing.T) {
	c, mb := overrideTestChannel(t, map[string]string{"group:g-internal": "pws"})
	c.HandleAuthorizedMessage("anyone", "g-internal", "hi", nil, nil, "group")
	msg := consumeOne(t, mb)
	if msg.AgentID != "pws" {
		t.Fatalf("listed group AgentID = %q, want override target", msg.AgentID)
	}
	if got := msg.Metadata[MetaAgentIDFallback]; got != "sales-agent" {
		t.Fatalf("fallback metadata = %q, want the instance default", got)
	}
}

func TestAgentOverrideGroupUnlistedKeepsDefault(t *testing.T) {
	c, mb := overrideTestChannel(t, map[string]string{"group:g-internal": "pws"})
	c.HandleAuthorizedMessage("anyone", "g-other", "hi", nil, nil, "group")
	msg := consumeOne(t, mb)
	if msg.AgentID != "sales-agent" {
		t.Fatalf("unlisted group AgentID = %q, want the instance default", msg.AgentID)
	}
}

// A group message from a sender who is ALSO user-listed must route by the
// group, not the sender — an internal staffer writing inside a customer-facing
// group speaks in that group's context.
func TestAgentOverrideGroupWinsOverSenderInGroups(t *testing.T) {
	c, mb := overrideTestChannel(t, map[string]string{"user:staff-1": "pws"})
	c.HandleAuthorizedMessage("staff-1", "g-customer", "hi", nil, nil, "group")
	msg := consumeOne(t, mb)
	if msg.AgentID != "sales-agent" {
		t.Fatalf("group message keyed by sender: AgentID = %q, want the instance default", msg.AgentID)
	}
}

// Legacy compound sender IDs ("uid|username") must match a "user:uid" key —
// the funnel already derives userID by stripping the suffix.
func TestAgentOverrideMatchesCompoundSenderID(t *testing.T) {
	c, mb := overrideTestChannel(t, map[string]string{"user:staff-1": "pws"})
	c.HandleAuthorizedMessage("staff-1|Le Van A", "staff-1", "hi", nil, nil, "direct")
	msg := consumeOne(t, mb)
	if msg.AgentID != "pws" {
		t.Fatalf("compound sender AgentID = %q, want override target", msg.AgentID)
	}
}

// An override naming the default agent is a no-op: no fallback metadata, so
// the consumer never logs a spurious degradation.
func TestAgentOverrideEqualToDefaultIsNoOp(t *testing.T) {
	c, mb := overrideTestChannel(t, map[string]string{"user:staff-1": "sales-agent"})
	c.HandleAuthorizedMessage("staff-1", "staff-1", "hi", nil, nil, "direct")
	msg := consumeOne(t, mb)
	if msg.AgentID != "sales-agent" {
		t.Fatalf("AgentID = %q", msg.AgentID)
	}
	if msg.Metadata[MetaAgentIDFallback] != "" {
		t.Fatal("self-override must not stamp fallback metadata")
	}
}

func TestAgentOverrideNilMapUnchangedBehavior(t *testing.T) {
	c, mb := overrideTestChannel(t, nil)
	c.HandleAuthorizedMessage("anyone", "anyone", "hi", nil, nil, "direct")
	msg := consumeOne(t, mb)
	if msg.AgentID != "sales-agent" {
		t.Fatalf("AgentID = %q, want the instance default", msg.AgentID)
	}
	if msg.Metadata != nil {
		t.Fatalf("nil metadata must stay nil when no override fires, got %v", msg.Metadata)
	}
}
