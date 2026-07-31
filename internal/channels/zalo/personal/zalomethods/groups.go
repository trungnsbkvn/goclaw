package zalomethods

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/gateway"
	goclawprotocol "github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// GroupMethods exposes Zalo group administration over the gateway RPC.
//
// Unlike ContactsMethods, these run against the LIVE channel session rather
// than logging in with stored credentials per call. Two reasons: a login per
// group operation is the kind of repeated-auth pattern that gets a Zalo account
// flagged, and the channel is by definition already connected whenever an agent
// is in a position to create a group.
//
// maxGroupMembers bounds a single call. It is not a Zalo limit — it is a blast
// radius limit: the caller is an automated agent, and the difference between a
// bug that adds 3 wrong people and one that adds 300 is worth a constant.
const maxGroupMembers = 50

type GroupMethods struct {
	channelMgr *channels.Manager
}

func NewGroupMethods(mgr *channels.Manager) *GroupMethods {
	return &GroupMethods{channelMgr: mgr}
}

func (m *GroupMethods) Register(router *gateway.MethodRouter) {
	router.Register(goclawprotocol.MethodZaloGroupCreate, m.handleCreate)
	router.Register(goclawprotocol.MethodZaloGroupAddMembers, m.handleAddMembers)
	router.Register(goclawprotocol.MethodZaloGroupRemoveMembers, m.handleRemoveMembers)
	router.Register(goclawprotocol.MethodZaloGroupInviteLink, m.handleInviteLink)
	router.Register(goclawprotocol.MethodZaloServicesList, m.handleServicesList)
}

// groupParams is the shared request shape. channel defaults to the standard
// zalo_personal channel name so the common case needs no argument.
type groupParams struct {
	Channel  string   `json:"channel"`
	GroupID  string   `json:"group_id"`
	Name     string   `json:"name"`
	Members  []string `json:"members"`
	WithLink bool     `json:"with_link"`
}

func (p *groupParams) channelName() string {
	if p.Channel != "" {
		return p.Channel
	}
	return channels.TypeZaloPersonal
}

func parseGroupParams(req *goclawprotocol.RequestFrame) groupParams {
	var p groupParams
	if req.Params != nil {
		_ = json.Unmarshal(req.Params, &p)
	}
	return p
}

// validateMembers rejects the shapes that would otherwise fail deep inside the
// protocol layer or, worse, succeed at a size nobody intended.
func validateMembers(members []string) string {
	if len(members) == 0 {
		return "members must not be empty"
	}
	if len(members) > maxGroupMembers {
		return "too many members in one call"
	}
	for _, id := range members {
		if id == "" {
			return "members must not contain an empty id"
		}
	}
	return ""
}

func (m *GroupMethods) handleCreate(ctx context.Context, client *gateway.Client, req *goclawprotocol.RequestFrame) {
	p := parseGroupParams(req)
	if p.Name == "" {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInvalidRequest, "name is required"))
		return
	}
	if msg := validateMembers(p.Members); msg != "" {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInvalidRequest, msg))
		return
	}

	res, err := m.channelMgr.CreateGroup(ctx, p.channelName(), p.Name, p.Members, p.WithLink)
	if err != nil {
		slog.Warn("zalo group create failed", "channel", p.channelName(), "error", err)
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInternal, err.Error()))
		return
	}

	// error_members is reported on the SUCCESS path on purpose: Zalo creates
	// the group and refuses individual invitees, so this is a normal outcome
	// the caller must act on, not an error to retry.
	client.SendResponse(goclawprotocol.NewOKResponse(req.ID, map[string]any{
		"group_id":      res.GroupID,
		"link":          res.Link,
		"error_members": res.ErrorMembers,
	}))
}

func (m *GroupMethods) handleAddMembers(ctx context.Context, client *gateway.Client, req *goclawprotocol.RequestFrame) {
	p := parseGroupParams(req)
	if p.GroupID == "" {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInvalidRequest, "group_id is required"))
		return
	}
	if msg := validateMembers(p.Members); msg != "" {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInvalidRequest, msg))
		return
	}

	failed, err := m.channelMgr.AddGroupMembers(ctx, p.channelName(), p.GroupID, p.Members)
	if err != nil {
		slog.Warn("zalo group addMembers failed", "channel", p.channelName(), "group", p.GroupID, "error", err)
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInternal, err.Error()))
		return
	}
	client.SendResponse(goclawprotocol.NewOKResponse(req.ID, map[string]any{"error_members": failed}))
}

func (m *GroupMethods) handleRemoveMembers(ctx context.Context, client *gateway.Client, req *goclawprotocol.RequestFrame) {
	p := parseGroupParams(req)
	if p.GroupID == "" {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInvalidRequest, "group_id is required"))
		return
	}
	if msg := validateMembers(p.Members); msg != "" {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInvalidRequest, msg))
		return
	}

	failed, err := m.channelMgr.RemoveGroupMembers(ctx, p.channelName(), p.GroupID, p.Members)
	if err != nil {
		slog.Warn("zalo group removeMembers failed", "channel", p.channelName(), "group", p.GroupID, "error", err)
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInternal, err.Error()))
		return
	}
	client.SendResponse(goclawprotocol.NewOKResponse(req.ID, map[string]any{"error_members": failed}))
}

func (m *GroupMethods) handleInviteLink(ctx context.Context, client *gateway.Client, req *goclawprotocol.RequestFrame) {
	p := parseGroupParams(req)
	if p.GroupID == "" {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInvalidRequest, "group_id is required"))
		return
	}

	link, err := m.channelMgr.GroupInviteLink(ctx, p.channelName(), p.GroupID)
	if err != nil {
		slog.Warn("zalo group inviteLink failed", "channel", p.channelName(), "group", p.GroupID, "error", err)
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInternal, err.Error()))
		return
	}
	client.SendResponse(goclawprotocol.NewOKResponse(req.ID, map[string]any{"link": link}))
}

// handleServicesList answers "what can this account actually do?" from the live
// session, which on an undocumented protocol is the only honest way to find out
// short of shipping a guessed endpoint and reading the failure.
func (m *GroupMethods) handleServicesList(ctx context.Context, client *gateway.Client, req *goclawprotocol.RequestFrame) {
	p := parseGroupParams(req)

	names, err := m.channelMgr.ServiceNames(ctx, p.channelName())
	if err != nil {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInternal, err.Error()))
		return
	}
	client.SendResponse(goclawprotocol.NewOKResponse(req.ID, map[string]any{"services": names}))
}
