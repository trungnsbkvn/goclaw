package personal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
	"github.com/nextlevelbuilder/goclaw/internal/systemmessages"
)

const pairingDebounce = 60 * time.Second

// checkDMPolicy enforces DM policy for incoming messages.
func (c *Channel) checkDMPolicy(ctx context.Context, senderID, chatID string) bool {
	result := c.CheckDMPolicy(ctx, senderID, c.config.DMPolicy)
	switch result {
	case channels.PolicyAllow:
		return true
	case channels.PolicyNeedsPairing:
		c.sendPairingReply(ctx, senderID, chatID)
		return false
	default:
		slog.Debug("zalo_personal DM rejected by policy", "sender_id", senderID, "policy", c.config.DMPolicy)
		return false
	}
}

// checkGroupPolicy enforces group access policy (allowlist/pairing).
// Returns false if the group is blocked by policy; does NOT check @mention gating.
func (c *Channel) checkGroupPolicy(ctx context.Context, senderID, groupID string) bool {
	result := c.CheckGroupPolicy(ctx, senderID, groupID, c.config.GroupPolicy)
	switch result {
	case channels.PolicyAllow:
		return true
	case channels.PolicyNeedsPairing:
		groupSenderID := fmt.Sprintf("group:%s", groupID)
		c.sendPairingReply(ctx, groupSenderID, groupID)
		return false
	default:
		slog.Debug("zalo_personal group message rejected by policy", "group_id", groupID, "policy", c.config.GroupPolicy)
		return false
	}
}

// pairingNoticeEnabled reports whether the pairing code may be sent into the
// chat. Unset means NO on this channel — see ZaloPersonalConfig.PairingNotice.
func (c *Channel) pairingNoticeEnabled() bool {
	return c.config.PairingNotice != nil && *c.config.PairingNotice
}

// sendPairingReply records the pairing request and — only when the instance
// opts in — tells the sender about it.
//
// The request is ALWAYS recorded, whether or not anything is sent: that is what
// puts the sender in pairing.list for an operator to approve. What
// config.PairingNotice gates is purely whether the pairing code is broadcast
// into the chat that triggered it. See ZaloPersonalConfig.PairingNotice for why
// this channel defaults to silent.
func (c *Channel) sendPairingReply(ctx context.Context, senderID, chatID string) {
	ps := c.PairingService()
	sess := c.session()
	if ps == nil || sess == nil {
		return
	}

	if !c.CanSendPairingNotif(senderID, pairingDebounce) {
		return
	}

	code, err := ps.RequestPairing(ctx, senderID, c.Name(), chatID, "default", nil)
	if err != nil {
		slog.Debug("zalo_personal pairing request failed", "sender_id", senderID, "error", err)
		return
	}

	if !c.pairingNoticeEnabled() {
		// Logged at Info, not Debug: this is the ONLY place an operator learns
		// that someone is waiting, now that the sender is not told to go and
		// fetch them. It carries everything needed to approve.
		slog.Info("zalo_personal: unpaired sender held for operator approval "+
			"(no notice sent — see pairing_notice)",
			"channel", c.Name(), "sender_id", senderID, "chat_id", chatID, "code", code)
		c.MarkPairingNotifSent(senderID)
		return
	}

	replyText := c.SystemMessage("", systemmessages.KeyPairingAccountRequired, systemmessages.Vars{
		"platform":  "Zalo",
		"sender_id": senderID,
		"code":      code,
	})

	threadType := protocol.ThreadTypeUser
	if strings.HasPrefix(senderID, "group:") {
		threadType = protocol.ThreadTypeGroup
	}

	sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := protocol.SendMessage(sendCtx, sess, chatID, threadType, replyText); err != nil {
		slog.Warn("zalo_personal: failed to send pairing reply", "error", err)
	} else {
		c.MarkPairingNotifSent(senderID)
		slog.Info("zalo_personal pairing reply sent", "sender_id", senderID, "code", code)
	}
}

// checkBotMentioned reports whether the bot is @mentioned in the message.
func (c *Channel) checkBotMentioned(mentions []*protocol.TMention) bool {
	sess := c.session()
	if sess == nil {
		return false
	}
	return isBotMentioned(sess.UID, mentions)
}

// isBotMentioned checks if the bot's UID is @mentioned in the message.
// Filters out @all mentions (Type=1, UID="-1") — only targeted @bot counts.
func isBotMentioned(botUID string, mentions []*protocol.TMention) bool {
	if botUID == "" {
		return false
	}

	for _, m := range mentions {
		if m == nil {
			continue
		}
		if m.Type == protocol.MentionAll || m.UID == protocol.MentionAllUID {
			continue
		}
		if m.UID == botUID {
			return true
		}
	}
	return false
}
