package personal

import (
	"context"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

// Group administration on the LIVE session.
//
// Deliberately not the temporary-login pattern that zalomethods/contacts.go
// uses: every login is an event Zalo sees, and a bot account that re-logs-in
// once per group operation is producing exactly the signal that gets an account
// flagged. The channel is already authenticated whenever the agent is in a
// position to escalate, so these reuse that session.
//
// A newly created group is registered with MarkGroupApproved for the same
// reason ListGroups does it: without the cache entry, Send() cannot tell a
// group ID from a user ID for a group that has never sent us a message — and a
// group we just created has by definition never sent us anything. The very
// first message into the new group would otherwise be delivered as a DM to a
// non-existent user and vanish with no error.

// Compile-time proof the channel still satisfies both capabilities. Manager
// reaches these through a type assertion, so a drifted signature would not fail
// the build — it would silently make the channel look like it never supported
// group administration, and the gateway would answer "does not support group
// administration" for a channel that plainly does.
var (
	_ channels.GroupAdminProvider     = (*Channel)(nil)
	_ channels.ServiceCatalogProvider = (*Channel)(nil)
)

// CreateGroup implements channels.GroupAdminProvider.
func (c *Channel) CreateGroup(ctx context.Context, name string, memberIDs []string, withLink bool) (*channels.GroupCreateResult, error) {
	sess := c.session()
	if sess == nil {
		return nil, fmt.Errorf("zalo_personal: not connected")
	}

	res, err := protocol.CreateGroup(ctx, sess, name, memberIDs, withLink)
	if err != nil {
		return nil, err
	}
	c.MarkGroupApproved(res.GroupID)

	return &channels.GroupCreateResult{
		GroupID:      res.GroupID,
		Link:         res.Link,
		ErrorMembers: res.ErrorMembers,
	}, nil
}

// AddGroupMembers implements channels.GroupAdminProvider. The returned slice
// lists members Zalo refused — commonly because they are not friends of the
// account, which is the normal case for a customer. A nil error does NOT mean
// everyone joined.
func (c *Channel) AddGroupMembers(ctx context.Context, groupID string, memberIDs []string) ([]string, error) {
	sess := c.session()
	if sess == nil {
		return nil, fmt.Errorf("zalo_personal: not connected")
	}
	res, err := protocol.AddGroupMembers(ctx, sess, groupID, memberIDs)
	if err != nil {
		return nil, err
	}
	c.MarkGroupApproved(groupID)
	return res.ErrorMembers, nil
}

// RemoveGroupMembers implements channels.GroupAdminProvider.
func (c *Channel) RemoveGroupMembers(ctx context.Context, groupID string, memberIDs []string) ([]string, error) {
	sess := c.session()
	if sess == nil {
		return nil, fmt.Errorf("zalo_personal: not connected")
	}
	res, err := protocol.RemoveGroupMembers(ctx, sess, groupID, memberIDs)
	if err != nil {
		return nil, err
	}
	return res.ErrorMembers, nil
}

// GroupInviteLink implements channels.GroupAdminProvider.
//
// Prefer the link CreateGroup already returned when withLink was set — this
// hits the least-verified of the group endpoints, and a group created with a
// link does not need a second call.
func (c *Channel) GroupInviteLink(ctx context.Context, groupID string) (string, error) {
	sess := c.session()
	if sess == nil {
		return "", fmt.Errorf("zalo_personal: not connected")
	}
	return protocol.GroupInviteLink(ctx, sess, groupID)
}

// ServiceNames implements channels.ServiceCatalogProvider — the services this
// account's session was actually offered at login.
//
// This is how "can this account send friend requests?" gets answered: by asking
// a live session, not by shipping a guessed endpoint and reading the failure.
func (c *Channel) ServiceNames(_ context.Context) ([]string, error) {
	sess := c.session()
	if sess == nil {
		return nil, fmt.Errorf("zalo_personal: not connected")
	}
	if sess.LoginInfo == nil {
		return nil, fmt.Errorf("zalo_personal: session has no login info")
	}
	return sess.LoginInfo.ZpwServiceMapV3.ServiceNames(), nil
}
