package methods

import (
	"context"
	"encoding/json"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/gateway"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// SendMethods handles the "send" RPC for routing outbound messages to channels.
// Matching TS src/gateway/server-methods/send.ts.
type SendMethods struct {
	msgBus *bus.MessageBus
}

func NewSendMethods(msgBus *bus.MessageBus) *SendMethods {
	return &SendMethods{msgBus: msgBus}
}

func (m *SendMethods) Register(router *gateway.MethodRouter) {
	router.Register(protocol.MethodSend, m.handleSend)
}

func (m *SendMethods) handleSend(ctx context.Context, client *gateway.Client, req *protocol.RequestFrame) {
	locale := store.LocaleFromContext(ctx)
	var params struct {
		Channel string `json:"channel"`
		To      string `json:"to"`
		Message string `json:"message"`
		// Media rides along so a caller can attach a real file.
		//
		// bus.OutboundMessage has carried this field all along and the Zalo personal
		// channel already consumes it (send.go → sendImage/sendFile), but THIS method
		// dropped it on the floor: it built an OutboundMessage with Channel/ChatID/
		// Content only. So the gateway's own "send" was text-only while the transport
		// under it could send documents — invisible from either end, because nothing
		// errors when a field is silently not forwarded.
		//
		// `url` is a path on the machine running the channel, not a remote URL: the
		// Zalo uploader takes a local file. jus-hub and this sidecar run on the same
		// Windows host, which is what makes that workable.
		Media []bus.MediaAttachment `json:"media,omitempty"`
	}
	if req.Params != nil {
		json.Unmarshal(req.Params, &params)
	}

	if params.Channel == "" {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "channel")))
		return
	}
	if params.To == "" {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "to")))
		return
	}
	// A message with attachments but no text is legitimate — sending a document with
	// no covering sentence is a normal thing to do — so the text requirement only
	// holds when there is nothing else to deliver.
	if params.Message == "" && len(params.Media) == 0 {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgMsgRequired)))
		return
	}

	m.msgBus.PublishOutbound(bus.OutboundMessage{
		Channel: params.Channel,
		ChatID:  params.To,
		Content: params.Message,
		Media:   params.Media,
	})

	client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
		"ok":      true,
		"channel": params.Channel,
		"to":      params.To,
	}))
}
