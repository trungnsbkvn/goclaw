package personal

import (
	"fmt"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

// Human-readable placeholders for Zalo messages that carry no downloadable
// payload — stickers, locations, contact cards, and whatever Zalo adds next.
//
// Why a describer rather than a type switch: goclaw's content extraction sniffed
// for a URL and returned empty when it found none, and the message handlers drop
// empty content before publishing. So every one of these message shapes was
// silently discarded — no log, no metric, no reply. A customer sending their
// location to ask "are you near me?" got absolute silence from the bot.
//
// An exhaustive switch over Zalo's msgType strings would fix today's cases and
// re-break the moment Zalo ships a new one, because the failure mode of a stale
// list is exactly the silent drop being removed. So: recognise what we can name
// precisely, and fall back to a generic-but-honest description for everything
// else. The agent can then respond sensibly instead of going mute.

// zaloTypeLabels maps the msgType values we can name confidently.
// Anything absent falls through to the generic branch — deliberately.
var zaloTypeLabels = map[string]string{
	"chat.sticker":      "nhãn dán",
	"chat.photo":        "hình ảnh",
	"chat.gif":          "ảnh động",
	"chat.voice":        "tin nhắn thoại",
	"chat.video.msg":    "video",
	"share.file":        "tệp",
	"chat.recommended":  "danh thiếp",
	"chat.location.new": "vị trí",
	"share.location":    "vị trí",
}

// describeNonMediaContent renders a placeholder for content we cannot download.
// Returns "" only when there is genuinely nothing to say, which the caller logs.
func describeNonMediaContent(att *protocol.Attachment, msgType string) string {
	// Location is the one case where the payload IS the information, so render
	// the coordinates rather than a label — the agent can actually use them.
	if att.HasLocation() {
		var b strings.Builder
		b.WriteString("[Khách chia sẻ vị trí")
		if title := strings.TrimSpace(att.Title); title != "" {
			b.WriteString(": ")
			b.WriteString(title)
		}
		b.WriteString("]")
		b.WriteString(fmt.Sprintf("\nToạ độ: %s, %s", att.Latitude, att.Longitude))
		b.WriteString(fmt.Sprintf("\nBản đồ: https://maps.google.com/?q=%s,%s", att.Latitude, att.Longitude))
		return b.String()
	}

	label := zaloTypeLabels[strings.TrimSpace(msgType)]

	// Any descriptive text Zalo attached is more useful than our label.
	var detail string
	if att != nil {
		detail = strings.TrimSpace(att.Title)
		if detail == "" {
			detail = strings.TrimSpace(att.Description)
		}
	}

	switch {
	case label != "" && detail != "":
		return fmt.Sprintf("[Khách gửi %s: %s]", label, detail)
	case label != "":
		return fmt.Sprintf("[Khách gửi %s]", label)
	case detail != "":
		return fmt.Sprintf("[Khách gửi một nội dung đính kèm: %s]", detail)
	case strings.TrimSpace(msgType) != "":
		// Unknown type, no detail — still say SOMETHING. Carrying the raw type
		// makes the gap visible in transcripts, which is how the next missing
		// label gets noticed.
		return fmt.Sprintf("[Khách gửi một nội dung goclaw chưa đọc được (%s)]", strings.TrimSpace(msgType))
	default:
		return ""
	}
}
