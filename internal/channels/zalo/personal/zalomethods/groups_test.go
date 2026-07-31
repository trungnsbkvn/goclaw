package zalomethods

import (
	"encoding/json"
	"strings"
	"testing"

	goclawprotocol "github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// validateMembers is the blast-radius guard between an automated agent and a
// write on the firm's Zalo account. The caller is a model-driven loop, so the
// difference between a bug that adds 3 wrong people and one that adds 300 is
// this function.

func TestValidateMembers(t *testing.T) {
	tests := []struct {
		name    string
		members []string
		wantErr bool
	}{
		{"normal", []string{"u1", "u2"}, false},
		{"exactly at the cap", make([]string, maxGroupMembers), true}, // empty ids
		{"empty list", nil, true},
		{"over the cap", make([]string, maxGroupMembers+1), true},
		{"contains an empty id", []string{"u1", ""}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := validateMembers(tc.members)
			if (got != "") != tc.wantErr {
				t.Errorf("validateMembers(%d members) = %q, wantErr=%v", len(tc.members), got, tc.wantErr)
			}
		})
	}
}

// A full-size batch of REAL ids must pass — a cap that rejects its own limit
// would silently halve the usable batch size.
func TestValidateMembersAcceptsFullBatch(t *testing.T) {
	members := make([]string, maxGroupMembers)
	for i := range members {
		members[i] = "uid"
	}
	if msg := validateMembers(members); msg != "" {
		t.Errorf("a batch exactly at the cap must pass, got %q", msg)
	}

	members = append(members, "one-too-many")
	if msg := validateMembers(members); msg == "" {
		t.Error("one over the cap must be rejected")
	}
}

// channel defaults to zalo_personal so the common call needs no argument, but
// an explicit channel must win — a deployment can run more than one instance.
func TestGroupParamsChannelDefault(t *testing.T) {
	if got := (&groupParams{}).channelName(); got == "" {
		t.Error("channel must default, not be empty")
	}
	if got := (&groupParams{Channel: "zalo-sales"}).channelName(); got != "zalo-sales" {
		t.Errorf("explicit channel = %q, want zalo-sales", got)
	}
}

// Malformed params must not panic the gateway — they arrive over the wire.
func TestParseGroupParamsToleratesGarbage(t *testing.T) {
	for _, raw := range []string{``, `null`, `{}`, `{"members":"not-an-array"}`, `not json at all`} {
		var frame goclawprotocol.RequestFrame
		if raw != "" {
			frame.Params = json.RawMessage(raw)
		}
		p := parseGroupParams(&frame) // must not panic
		if p.GroupID != "" {
			t.Errorf("garbage %q produced a group id %q", raw, p.GroupID)
		}
	}
}

// A well-formed request must actually decode — the tolerance above must not be
// hiding a parser that always returns zero values.
func TestParseGroupParamsDecodesRealRequest(t *testing.T) {
	frame := goclawprotocol.RequestFrame{
		Params: json.RawMessage(`{"channel":"zalo-sales","group_id":"g-1","name":"Y&P","members":["u1","u2"],"with_link":true}`),
	}
	p := parseGroupParams(&frame)
	if p.Channel != "zalo-sales" || p.GroupID != "g-1" || p.Name != "Y&P" {
		t.Errorf("decoded = %+v", p)
	}
	if len(p.Members) != 2 || !p.WithLink {
		t.Errorf("members=%v withLink=%v", p.Members, p.WithLink)
	}
}

// Every group write must be classified as a write in the permission policy, or
// MethodRole's fail-closed default denies it to everyone including the owner —
// a symptom that reads like a routing bug rather than a policy gap.
func TestGroupMethodNamesAreNamespaced(t *testing.T) {
	for _, m := range []string{
		goclawprotocol.MethodZaloGroupCreate,
		goclawprotocol.MethodZaloGroupAddMembers,
		goclawprotocol.MethodZaloGroupRemoveMembers,
		goclawprotocol.MethodZaloGroupInviteLink,
	} {
		if !strings.HasPrefix(m, "zalo.group.") {
			t.Errorf("method %q should live under zalo.group.", m)
		}
	}
	if !strings.HasPrefix(goclawprotocol.MethodZaloServicesList, "zalo.") {
		t.Errorf("method %q should live under zalo.", goclawprotocol.MethodZaloServicesList)
	}
}
