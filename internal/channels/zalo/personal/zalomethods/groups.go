package zalomethods

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

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
	router.Register(goclawprotocol.MethodZaloSelf, m.handleSelf)
	router.Register(goclawprotocol.MethodZaloFriendFind, m.handleFriendFind)
	router.Register(goclawprotocol.MethodZaloFriendRequest, m.handleFriendRequest)
	router.Register(goclawprotocol.MethodZaloFriendList, m.handleFriendList)
}

// friendParams is the request shape for the contact methods.
type friendParams struct {
	Channel string `json:"channel"`
	Phone   string `json:"phone"`
	UserID  string `json:"user_id"`
	Message string `json:"message"`
}

func (p *friendParams) channelName() string {
	if p.Channel != "" {
		return p.Channel
	}
	return channels.TypeZaloPersonal
}

func parseFriendParams(req *goclawprotocol.RequestFrame) friendParams {
	var p friendParams
	if req.Params != nil {
		_ = json.Unmarshal(req.Params, &p)
	}
	return p
}

// handleFriendFind resolves a phone number to a Zalo account.
//
// "not found" is returned as a SUCCESS with found=false rather than an error:
// a number that is not on Zalo is an ordinary outcome, and an error response
// would make callers unable to tell it apart from an outage.
func (m *GroupMethods) handleFriendFind(ctx context.Context, client *gateway.Client, req *goclawprotocol.RequestFrame) {
	p := parseFriendParams(req)
	if p.Phone == "" {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInvalidRequest, "phone is required"))
		return
	}

	handle, err := m.channelMgr.FindByPhone(ctx, p.channelName(), p.Phone)
	if err != nil {
		if strings.Contains(err.Error(), "no Zalo account") {
			client.SendResponse(goclawprotocol.NewOKResponse(req.ID, map[string]any{"found": false}))
			return
		}
		slog.Warn("zalo friend find failed", "channel", p.channelName(), "error", err)
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInternal, err.Error()))
		return
	}
	client.SendResponse(goclawprotocol.NewOKResponse(req.ID, map[string]any{
		"found": true, "user_id": handle.UserID, "display_name": handle.DisplayName,
	}))
}

// handleFriendRequest sends a friend request.
//
// GoClaw applies no rate limit here on purpose: it cannot tell one request to a
// customer who asked to be contacted from a loop. That policy belongs to the
// caller, which knows the conversation. The log line exists so the calls are at
// least reconstructable after the fact.
func (m *GroupMethods) handleFriendRequest(ctx context.Context, client *gateway.Client, req *goclawprotocol.RequestFrame) {
	p := parseFriendParams(req)
	if p.UserID == "" {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInvalidRequest, "user_id is required"))
		return
	}

	slog.Info("zalo friend request", "channel", p.channelName(), "user", p.UserID)
	if err := m.channelMgr.SendFriendRequest(ctx, p.channelName(), p.UserID, p.Message); err != nil {
		slog.Warn("zalo friend request failed", "channel", p.channelName(), "user", p.UserID, "error", err)
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInternal, err.Error()))
		return
	}
	client.SendResponse(goclawprotocol.NewOKResponse(req.ID, map[string]any{"ok": true}))
}

// handleFriendList lists the account's friends — the way to detect that a
// friend request was accepted, since no accepted-event is delivered.
func (m *GroupMethods) handleFriendList(ctx context.Context, client *gateway.Client, req *goclawprotocol.RequestFrame) {
	p := parseFriendParams(req)

	friends, err := m.channelMgr.ListFriends(ctx, p.channelName())
	if err != nil {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInternal, err.Error()))
		return
	}
	client.SendResponse(goclawprotocol.NewOKResponse(req.ID, map[string]any{"friends": friends}))
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

// handleSelf reports the connected account's own uid.
func (m *GroupMethods) handleSelf(ctx context.Context, client *gateway.Client, req *goclawprotocol.RequestFrame) {
	p := parseGroupParams(req)

	uid, err := m.channelMgr.SelfUserID(ctx, p.channelName())
	if err != nil {
		client.SendResponse(goclawprotocol.NewErrorResponse(req.ID, goclawprotocol.ErrInternal, err.Error()))
		return
	}
	client.SendResponse(goclawprotocol.NewOKResponse(req.ID, map[string]any{"user_id": uid}))
}
