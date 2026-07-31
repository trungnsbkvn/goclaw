package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Group administration: create a group, invite members, remove members, and
// read a group's join link.
//
// ── VERIFICATION STATUS — read before debugging a failure here ──────────────
//
// This is an unofficial, reverse-engineered protocol. What is CONFIRMED (by
// three calls already working in this package) versus RECONSTRUCTED matters
// when something returns a non-zero error_code:
//
//	CONFIRMED  the "group" service base URL, the AES-CBC payload encryption,
//	           the zpw_ver/zpw_type query defaults, the url-encoded `params=`
//	           form body, and the double-envelope response decrypt. Every one of
//	           those is exercised today by FetchGroups (/api/group/getmg-v2),
//	           fetchGroupIDs (/api/group/getlg/v4) and SendMessage
//	           (/api/group/sendmsg). callGroupAPI below is exactly that shape,
//	           factored out — so a bug in the plumbing would already be breaking
//	           group messaging.
//
//	RECONSTRUCTED  the four API PATHS and their parameter names. They come from
//	           the zca-js client this package was ported from, not from a
//	           captured session. If one is wrong, Zalo answers with a non-zero
//	           error_code and the call fails LOUDLY with that code in the error
//	           — it does not corrupt anything or half-succeed. Fix the constant,
//	           no other change needed.
//
// Deliberately NOT implemented here: friend requests. The friend API needs a
// service key that this account's session may not advertise at all, and
// guessing both a service name and a path at once produces an error that cannot
// be diagnosed. ServiceNames() plus the zalo.services.list gateway method exist
// to answer that question from a live session first.
const (
	// pathGroupCreate creates a group. RECONSTRUCTED.
	pathGroupCreate = "/api/group/create/v2"
	// pathGroupInvite adds members to an existing group. RECONSTRUCTED.
	pathGroupInvite = "/api/group/invite/v2"
	// pathGroupKickout removes members from a group. RECONSTRUCTED.
	pathGroupKickout = "/api/group/kickout"
	// pathGroupLink reads/creates a group's join link. RECONSTRUCTED, and the
	// least certain of the four — prefer CreateGroup's own returned link.
	pathGroupLink = "/api/group/link/create"
)

// zsourceWeb is the client-origin marker Zalo web sends on group mutations.
const zsourceWeb = 601

// memberTypeUser is the per-member type flag paired with each uid in members[].
// Zalo expects a parallel array, not a list of objects.
const memberTypeUser = -1

// GroupCreateResult is the outcome of creating a group.
//
// Members can partially fail — Zalo creates the group and reports which uids it
// could not add (typically because they are not friends of the creator, or have
// restricted who may add them to groups). Callers MUST treat a populated
// ErrorMembers as a real, user-visible outcome rather than a detail: a group
// created without the person it was created for is worse than no group.
type GroupCreateResult struct {
	GroupID       string   `json:"groupId"`
	GroupType     int      `json:"groupType,omitempty"`
	SuccessMember []string `json:"-"`
	ErrorMembers  []string `json:"error_members,omitempty"`
	// Link is the join link, present only when CreateGroup was asked for one.
	Link string `json:"-"`
}

// rawGroupCreateResult mirrors the wire shape, including Zalo's "sucessMembers"
// misspelling — kept verbatim because it is what the server actually sends.
type rawGroupCreateResult struct {
	GroupID       any      `json:"groupId"`
	GroupType     int      `json:"groupType"`
	SuccessMember []string `json:"sucessMembers"`
	ErrorMembers  []string `json:"errorMembers"`
	Link          string   `json:"group_link"`
}

// GroupMemberChange is the outcome of an invite or removal.
type GroupMemberChange struct {
	ErrorMembers []string `json:"errorMembers"`
}

// CreateGroup creates a Zalo group owned by the authenticated account.
//
// withLink asks Zalo to mint a join link at creation time. That is the reliable
// way to get people into the group: adding a NON-FRIEND uid to a group is
// widely restricted, so an invite link the customer taps is the flow that
// works, while AddGroupMembers is best-effort for people already connected.
func CreateGroup(ctx context.Context, sess *Session, name string, memberIDs []string, withLink bool) (*GroupCreateResult, error) {
	if name == "" {
		return nil, fmt.Errorf("zalo_personal: group name cannot be empty")
	}
	// Zalo will not create a group with no members besides the creator.
	if len(memberIDs) == 0 {
		return nil, fmt.Errorf("zalo_personal: group needs at least one member")
	}

	createLink := 0
	if withLink {
		createLink = 1
	}
	payload := map[string]any{
		"clientId":     time.Now().UnixMilli(),
		"gname":        name,
		"gdesc":        "",
		"members":      memberIDs,
		"membersTypes": memberTypes(len(memberIDs)),
		"nameChanged":  1,
		"createLink":   createLink,
		"zsource":      zsourceWeb,
		"imei":         sess.IMEI,
	}

	data, err := callGroupAPI(ctx, sess, pathGroupCreate, payload)
	if err != nil {
		return nil, fmt.Errorf("zalo_personal: create group: %w", err)
	}

	var raw rawGroupCreateResult
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("zalo_personal: parse create group result: %w", err)
	}
	// groupId arrives as a number in some responses and a string in others.
	groupID := convertToString(raw.GroupID)
	if groupID == "" || groupID == "<nil>" {
		return nil, fmt.Errorf("zalo_personal: create group returned no group id")
	}
	return &GroupCreateResult{
		GroupID:       groupID,
		GroupType:     raw.GroupType,
		SuccessMember: raw.SuccessMember,
		ErrorMembers:  raw.ErrorMembers,
		Link:          raw.Link,
	}, nil
}

// AddGroupMembers invites uids into an existing group.
//
// Best-effort by nature: Zalo returns per-member failures in ErrorMembers
// rather than failing the call, so a nil error does NOT mean everyone joined.
func AddGroupMembers(ctx context.Context, sess *Session, groupID string, memberIDs []string) (*GroupMemberChange, error) {
	if groupID == "" {
		return nil, fmt.Errorf("zalo_personal: group id cannot be empty")
	}
	if len(memberIDs) == 0 {
		return nil, fmt.Errorf("zalo_personal: no members to add")
	}

	payload := map[string]any{
		"grid":         groupID,
		"members":      memberIDs,
		"membersTypes": memberTypes(len(memberIDs)),
		"imei":         sess.IMEI,
		"clientLang":   sess.Language,
	}

	data, err := callGroupAPI(ctx, sess, pathGroupInvite, payload)
	if err != nil {
		return nil, fmt.Errorf("zalo_personal: add group members: %w", err)
	}
	return parseMemberChange(data)
}

// RemoveGroupMembers kicks uids out of a group. Requires admin rights on the
// group, which the account has by default for groups it created.
func RemoveGroupMembers(ctx context.Context, sess *Session, groupID string, memberIDs []string) (*GroupMemberChange, error) {
	if groupID == "" {
		return nil, fmt.Errorf("zalo_personal: group id cannot be empty")
	}
	if len(memberIDs) == 0 {
		return nil, fmt.Errorf("zalo_personal: no members to remove")
	}

	payload := map[string]any{
		"grid":    groupID,
		"members": memberIDs,
		"imei":    sess.IMEI,
	}

	data, err := callGroupAPI(ctx, sess, pathGroupKickout, payload)
	if err != nil {
		return nil, fmt.Errorf("zalo_personal: remove group members: %w", err)
	}
	return parseMemberChange(data)
}

// GroupInviteLink returns a group's join link, minting one if needed.
//
// Prefer the Link that CreateGroup already returned — this path is the least
// verified of the four, and a group created with withLink=true has the answer
// without a second call.
func GroupInviteLink(ctx context.Context, sess *Session, groupID string) (string, error) {
	if groupID == "" {
		return "", fmt.Errorf("zalo_personal: group id cannot be empty")
	}

	payload := map[string]any{
		"grid": groupID,
		"imei": sess.IMEI,
	}

	data, err := callGroupAPI(ctx, sess, pathGroupLink, payload)
	if err != nil {
		return "", fmt.Errorf("zalo_personal: group invite link: %w", err)
	}

	// The link field name varies across responses; accept the known spellings
	// rather than pinning one and returning empty on the others.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("zalo_personal: parse group link: %w", err)
	}
	for _, key := range []string{"link", "group_link", "url"} {
		if v, ok := raw[key]; ok {
			if s := convertToString(v); s != "" && s != "<nil>" {
				return s, nil
			}
		}
	}
	return "", fmt.Errorf("zalo_personal: group link response had no link field")
}

// callGroupAPI performs the standard encrypted call against the group service.
//
// This is the shape already proven by fetchGroupDetails and SendMessage:
// encrypt the payload with the session key, add zpw_ver/zpw_type, POST it as a
// url-encoded `params` form field, then unwrap the OUTER envelope and decrypt
// the data field (which itself contains an inner envelope — decryptDataField
// handles that second unwrap).
func callGroupAPI(ctx context.Context, sess *Session, apiPath string, payload map[string]any) (json.RawMessage, error) {
	baseURL := getServiceURL(sess, "group")
	if baseURL == "" {
		return nil, fmt.Errorf("zalo_personal: no group service URL")
	}

	encData, err := encryptPayload(sess, payload)
	if err != nil {
		return nil, fmt.Errorf("encrypt payload: %w", err)
	}

	reqURL := makeURL(sess, baseURL+apiPath, nil, true)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL,
		buildFormBody(map[string]string{"params": encData}))
	if err != nil {
		return nil, err
	}
	setDefaultHeaders(req, sess)

	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var envelope Response[*string]
	if err := readJSON(resp, &envelope); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if envelope.ErrorCode != 0 {
		// A wrong RECONSTRUCTED path lands here — surface the code and message
		// verbatim so the constant can be corrected without a capture session.
		return nil, fmt.Errorf("error code %d: %s (path %s)",
			envelope.ErrorCode, envelope.ErrorMessage, apiPath)
	}
	if envelope.Data == nil {
		return nil, fmt.Errorf("empty response data (path %s)", apiPath)
	}
	return decryptDataField(sess, *envelope.Data)
}

// parseMemberChange reads the per-member failure list shared by invite/kick.
func parseMemberChange(data json.RawMessage) (*GroupMemberChange, error) {
	var out GroupMemberChange
	// Some responses are an empty array rather than an object on full success.
	if err := json.Unmarshal(data, &out); err != nil {
		return &GroupMemberChange{}, nil
	}
	return &out, nil
}

// memberTypes builds the parallel type array Zalo expects alongside members[].
func memberTypes(n int) []int {
	types := make([]int, n)
	for i := range types {
		types[i] = memberTypeUser
	}
	return types
}
