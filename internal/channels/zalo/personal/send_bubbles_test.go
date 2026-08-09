package personal

import (
	"strings"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

// Bubbles are a DIRECT-thread affordance: a human sends a few short messages,
// not a block — but a group is noisy enough without the bot triple-posting,
// and a short reply split three ways reads as nervous rather than human.
func TestBubbleParagraphs(t *testing.T) {
	long := strings.Repeat("Một câu đủ dài nói về nghĩa vụ hợp đồng lao động. ", 5)

	if got := bubbleParagraphs("Dạ em nghe ạ.", protocol.ThreadTypeUser); len(got) != 1 {
		t.Fatalf("short reply split into %d bubbles", len(got))
	}

	two := long + "\n\n" + long
	if got := bubbleParagraphs(two, protocol.ThreadTypeUser); len(got) != 2 {
		t.Fatalf("two paragraphs became %d bubbles", len(got))
	}
	// The SAME text in a group keeps the single-message shape.
	if got := bubbleParagraphs(two, protocol.ThreadTypeGroup); len(got) != 1 {
		t.Fatalf("a group reply was split into %d bubbles", len(got))
	}

	five := strings.Join([]string{long, long, long, long, long}, "\n\n")
	got := bubbleParagraphs(five, protocol.ThreadTypeUser)
	if len(got) != bubbleMaxCount {
		t.Fatalf("five paragraphs became %d bubbles, want %d with the tail merged", len(got), bubbleMaxCount)
	}
	// Nothing may be dropped: the tail bubble carries the rest.
	joined := strings.Join(got, "\n\n")
	if strings.Count(joined, "nghĩa vụ hợp đồng") != strings.Count(five, "nghĩa vụ hợp đồng") {
		t.Error("the splitter dropped content")
	}

	if got := bubbleParagraphs("", protocol.ThreadTypeUser); got != nil {
		t.Fatalf("empty input produced %d bubbles", len(got))
	}
}
