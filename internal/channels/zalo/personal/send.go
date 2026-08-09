package personal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/channels/typing"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

const maxTextLength = 2000

// Bubble rendering for 1:1 threads: a human does not send a four-paragraph
// block, they send a few short messages. The paragraph seams the model already
// wrote are the bubbles; a typing event plus a short pause between them is
// what makes the sequence read as composing rather than pasting.
const (
	// bubbleMinRunes — below this the text goes out whole; three tiny bubbles
	// reads as nervous, not human.
	bubbleMinRunes = 200
	// bubbleMaxCount caps the burst; the tail paragraphs merge into the last
	// bubble rather than being dropped.
	bubbleMaxCount = 3
	bubblePause    = 1500 * time.Millisecond
)

// bubbleParagraphs splits one outbound text into 1–3 paragraph bubbles for a
// DIRECT thread. Groups keep the single-message shape: a multi-party room is
// noisy enough without the bot triple-posting.
func bubbleParagraphs(text string, threadType protocol.ThreadType) []string {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	if threadType != protocol.ThreadTypeUser || len([]rune(t)) < bubbleMinRunes {
		return []string{t}
	}
	var paras []string
	for _, p := range strings.Split(t, "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			paras = append(paras, p)
		}
	}
	if len(paras) == 0 {
		return []string{t}
	}
	if len(paras) > bubbleMaxCount {
		tail := strings.Join(paras[bubbleMaxCount-1:], "\n\n")
		paras = append(paras[:bubbleMaxCount-1], tail)
	}
	return paras
}

// Send delivers an outbound message to a Zalo chat.
func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	sess := c.session()
	if !c.IsRunning() || sess == nil {
		return fmt.Errorf("zalo_personal channel not running")
	}

	// Strip markdown — Zalo does not support any markup rendering.
	msg.Content = zalo.StripMarkdown(msg.Content)

	// Stop typing indicator before sending response
	if ctrl, ok := c.typingCtrls.LoadAndDelete(msg.ChatID); ok {
		ctrl.(*typing.Controller).Stop()
	}

	threadType := protocol.ThreadTypeUser
	if c.IsGroupApproved(msg.ChatID) {
		threadType = protocol.ThreadTypeGroup
	} else if msg.Metadata != nil {
		if _, ok := msg.Metadata["group_id"]; ok {
			threadType = protocol.ThreadTypeGroup
			c.MarkGroupApproved(msg.ChatID)
		}
	}

	// Send media attachments.
	for _, media := range msg.Media {
		if protocol.IsImageFile(media.URL) {
			if err := c.sendImage(ctx, sess, msg.ChatID, threadType, media.URL, media.Caption); err != nil {
				slog.Warn("zalo_personal: failed to send image", "path", media.URL, "error", err)
			}
		} else {
			if err := c.sendFile(ctx, sess, msg.ChatID, threadType, media.URL); err != nil {
				slog.Warn("zalo_personal: failed to send file", "path", media.URL, "error", err)
			}
		}
	}

	// Send text content (if any remains after media).
	if msg.Content != "" {
		return c.sendChunkedText(ctx, sess, msg.ChatID, threadType, msg.Content)
	}
	return nil
}

// sendImage uploads and sends an image file to a Zalo thread.
func (c *Channel) sendImage(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, filePath, caption string) error {
	upload, err := protocol.UploadImage(ctx, sess, chatID, threadType, filePath)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	_, err = protocol.SendImage(ctx, sess, chatID, threadType, upload, caption)
	return err
}

// sendFile uploads and sends a file to a Zalo thread.
func (c *Channel) sendFile(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, filePath string) error {
	ln := c.getListener()
	if ln == nil {
		return fmt.Errorf("listener not available for file upload")
	}
	upload, err := protocol.UploadFile(ctx, sess, ln, chatID, threadType, filePath)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	_, err = protocol.SendFile(ctx, sess, chatID, threadType, upload)
	return err
}

func (c *Channel) sendChunkedText(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, text string) error {
	for i, bubble := range bubbleParagraphs(text, threadType) {
		if i > 0 {
			// Typing between bubbles is what separates "composing the next
			// thought" from "pasting a prepared block". Best-effort — a failed
			// typing event must not cost the message behind it.
			if err := protocol.SendTypingEvent(ctx, sess, chatID, threadType); err != nil {
				slog.Debug("zalo_personal: typing between bubbles failed", "error", err)
			}
			select {
			case <-time.After(bubblePause):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		// Length chunking stays inside each bubble: a paragraph longer than the
		// platform cap still has to split, bubbles or not.
		for _, chunk := range channels.ChunkMarkdown(bubble, maxTextLength) {
			if _, err := protocol.SendMessage(ctx, sess, chatID, threadType, chunk); err != nil {
				return err
			}
		}
	}
	return nil
}
