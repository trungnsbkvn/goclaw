package personal

import (
	"encoding/json"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/config"
)

// Same silent-drop trap as pairing_notice: zaloInstanceConfig is the ONLY
// thing read out of a DB instance's JSON config, so agent_overrides must be
// listed there or the routing map an operator sets on the instance simply
// does not exist — no error, every thread keeps the default agent, and one
// account can never host a second persona.
func TestInstanceConfigCarriesAgentOverrides(t *testing.T) {
	var ic zaloInstanceConfig
	raw := []byte(`{"dm_policy":"open","agent_overrides":{"user:123":"pws","group:g1":"pws"}}`)
	if err := json.Unmarshal(raw, &ic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ic.AgentOverrides) != 2 {
		t.Fatalf("agent_overrides = %v, want 2 entries — the map was dropped", ic.AgentOverrides)
	}
	if ic.AgentOverrides["user:123"] != "pws" || ic.AgentOverrides["group:g1"] != "pws" {
		t.Fatalf("agent_overrides = %v", ic.AgentOverrides)
	}
}

// The second leg of the same trap: the map must survive the copy from
// zaloInstanceConfig into config.ZaloPersonalConfig and land on the
// BaseChannel via New() — pairing_notice shipped broken by missing exactly
// one of these copy sites.
func TestNewWiresAgentOverridesOntoBaseChannel(t *testing.T) {
	cfg := config.ZaloPersonalConfig{
		Enabled:        true,
		AgentOverrides: map[string]string{"user:123": "pws"},
	}
	ch, err := New(cfg, bus.New(), nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := ch.AgentOverrides()
	if got["user:123"] != "pws" {
		t.Fatalf("BaseChannel overrides = %v, want the config map wired through", got)
	}
}
