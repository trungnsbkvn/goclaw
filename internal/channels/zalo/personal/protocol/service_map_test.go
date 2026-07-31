package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The service map is server-driven and undocumented. Retaining only the five
// named fields made "does this session expose service X?" unanswerable without
// a code change, which is the wrong loop for a reverse-engineered protocol —
// these tests lock in that every advertised key survives parsing.

func TestServiceMapKeepsUnnamedKeys(t *testing.T) {
	raw := `{
		"chat": ["https://tt-chat1.chat.zalo.me"],
		"group": ["https://tt-group1.chat.zalo.me"],
		"file": ["https://tt-files.chat.zalo.me"],
		"profile": ["https://tt-profile.chat.zalo.me"],
		"group_poll": ["https://tt-grouppoll.chat.zalo.me"],
		"friend": ["https://tt-friend.chat.zalo.me"],
		"label": ["https://tt-label.chat.zalo.me"]
	}`

	var m ZpwServiceMapV3
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Named fields still work.
	if len(m.Group) != 1 || m.Group[0] != "https://tt-group1.chat.zalo.me" {
		t.Errorf("Group = %v", m.Group)
	}
	// Unnamed keys are retained — this is what makes the friend API knowable.
	if got := m.Raw["friend"]; len(got) != 1 || got[0] != "https://tt-friend.chat.zalo.me" {
		t.Errorf("Raw[friend] = %v", got)
	}
	if got := m.Raw["label"]; len(got) != 1 {
		t.Errorf("Raw[label] = %v", got)
	}

	want := []string{"chat", "file", "friend", "group", "group_poll", "label", "profile"}
	if got := m.ServiceNames(); !reflect.DeepEqual(got, want) {
		t.Errorf("ServiceNames() = %v, want %v", got, want)
	}
}

// A key whose value is a bare string rather than an array must still resolve;
// pinning the array shape would silently drop the service.
func TestServiceMapAcceptsScalarEndpoint(t *testing.T) {
	var m ZpwServiceMapV3
	if err := json.Unmarshal([]byte(`{"friend":"https://one.example"}`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := m.Raw["friend"]; len(got) != 1 || got[0] != "https://one.example" {
		t.Errorf("Raw[friend] = %v", got)
	}
}

// This parses during LOGIN. An unfamiliar value in a service GoClaw never calls
// must not be able to fail authentication.
func TestServiceMapSkipsUnparseableEntries(t *testing.T) {
	var m ZpwServiceMapV3
	err := json.Unmarshal([]byte(`{"chat":["https://ok.example"],"weird":{"nested":true},"n":42}`), &m)
	if err != nil {
		t.Fatalf("an odd entry must not fail the whole parse: %v", err)
	}
	if len(m.Chat) != 1 {
		t.Errorf("Chat = %v, want the good entry to survive", m.Chat)
	}
	if _, ok := m.Raw["weird"]; ok {
		t.Error("an object-valued entry should be skipped, not coerced")
	}
}

func TestGetServiceURLFallsBackToAdvertisedMap(t *testing.T) {
	sess := &Session{LoginInfo: &LoginInfo{}}
	if err := json.Unmarshal([]byte(`{"chat":["https://c.example"],"friend":["https://f.example"]}`),
		&sess.LoginInfo.ZpwServiceMapV3); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Named service resolves through the switch as before.
	if got := getServiceURL(sess, "chat"); got != "https://c.example" {
		t.Errorf("chat = %q", got)
	}
	// Unnamed service now resolves instead of silently returning "".
	if got := getServiceURL(sess, "friend"); got != "https://f.example" {
		t.Errorf("friend = %q, want the advertised endpoint", got)
	}
	// A genuinely absent service still returns "" — but now with a warning that
	// names what WAS advertised, which is what distinguishes "GoClaw doesn't
	// know this name" from "this account lacks the service".
	if got := getServiceURL(sess, "nope"); got != "" {
		t.Errorf("nope = %q, want empty", got)
	}
}

func TestGetServiceURLWithoutLoginInfo(t *testing.T) {
	if got := getServiceURL(&Session{}, "group"); got != "" {
		t.Errorf("got %q, want empty for an unauthenticated session", got)
	}
}
