package personal

import (
	"strings"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

// Before this, any Zalo message with no downloadable URL — sticker, location,
// contact card — produced empty content, and the handlers' `if content == ""`
// guard dropped it before publishing with no log anywhere. The customer got
// absolute silence. These tests lock in that nothing is silently dropped.

func TestDescribeLocationRendersCoordinates(t *testing.T) {
	att := &protocol.Attachment{
		Title:     "Quán cà phê",
		Latitude:  "10.7769",
		Longitude: "106.7009",
	}
	got := describeNonMediaContent(att, "share.location")

	for _, want := range []string{"vị trí", "10.7769", "106.7009", "maps.google.com", "Quán cà phê"} {
		if !strings.Contains(got, want) {
			t.Errorf("location description missing %q; got:\n%s", want, got)
		}
	}
}

func TestDescribeLocationWithoutTitle(t *testing.T) {
	att := &protocol.Attachment{Latitude: "21.02", Longitude: "105.83"}
	got := describeNonMediaContent(att, "chat.location.new")
	if !strings.Contains(got, "21.02") || !strings.Contains(got, "105.83") {
		t.Errorf("coordinates missing: %s", got)
	}
}

func TestDescribeKnownTypes(t *testing.T) {
	cases := map[string]string{
		"chat.sticker":     "nhãn dán",
		"chat.voice":       "tin nhắn thoại",
		"chat.recommended": "danh thiếp",
		"chat.gif":         "ảnh động",
	}
	for msgType, want := range cases {
		got := describeNonMediaContent(&protocol.Attachment{}, msgType)
		if !strings.Contains(got, want) {
			t.Errorf("msgType %q → %q, want it to mention %q", msgType, got, want)
		}
	}
}

// The important one: an UNKNOWN type must still produce something. A stale
// type-list that silently drops the next message shape Zalo ships is exactly the
// bug being fixed, so the fallback must never return empty when it has a type.
func TestDescribeUnknownTypeStillSaysSomething(t *testing.T) {
	got := describeNonMediaContent(&protocol.Attachment{}, "chat.some.future.thing")
	if strings.TrimSpace(got) == "" {
		t.Fatal("unknown msgType produced empty content — the message would be silently dropped")
	}
	// Carrying the raw type is how the missing label gets noticed later.
	if !strings.Contains(got, "chat.some.future.thing") {
		t.Errorf("unknown type should surface the raw type for diagnosis; got %q", got)
	}
}

func TestDescribePrefersAttachmentDetailOverLabel(t *testing.T) {
	att := &protocol.Attachment{Title: "Hợp đồng thuê nhà.pdf"}
	got := describeNonMediaContent(att, "share.file")
	if !strings.Contains(got, "Hợp đồng thuê nhà.pdf") {
		t.Errorf("attachment title should be surfaced; got %q", got)
	}
}

func TestDescribeEmptyIsEmpty(t *testing.T) {
	// Genuinely nothing to say → "" so the caller logs a warning rather than
	// publishing a meaningless placeholder.
	if got := describeNonMediaContent(nil, ""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// Zalo quotes numerics inconsistently — the same field arrives as `"10.77"` on
// one payload and `10.77` on another. A plain float64 made the whole content
// object fail to unmarshal on the quoted variant, which is how location messages
// used to disappear before they even reached the describer.
func TestStringOrNumberAcceptsBothJSONShapes(t *testing.T) {
	cases := map[string]string{
		`"10.7769"`: "10.7769",
		`10.7769`:   "10.7769",
		`null`:      "",
		`""`:        "",
	}
	for input, want := range cases {
		var s protocol.StringOrNumber
		if err := s.UnmarshalJSON([]byte(input)); err != nil {
			t.Errorf("UnmarshalJSON(%s) errored: %v", input, err)
			continue
		}
		if s.String() != want {
			t.Errorf("UnmarshalJSON(%s) = %q, want %q", input, s.String(), want)
		}
	}
}

func TestHasLocation(t *testing.T) {
	if (&protocol.Attachment{Latitude: "10", Longitude: "106"}).HasLocation() != true {
		t.Error("a full coordinate pair should report HasLocation")
	}
	for _, att := range []*protocol.Attachment{
		{Latitude: "10"},
		{Longitude: "106"},
		{},
		nil,
	} {
		if att.HasLocation() {
			t.Errorf("incomplete coordinates reported HasLocation: %+v", att)
		}
	}
}
