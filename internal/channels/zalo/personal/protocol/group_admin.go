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
//	VERIFIED   the four API paths and their parameter names, checked line by line
//	           against RFS-ADRENO/zca-js (src/apis/{createGroup,addUserToGroup,
//	           removeUserFromGroup,enableGroupLink}.ts) — the client this package
//	           was ported from. Three of the four matched the original
//	           reconstruction; the link endpoint did not, see pathGroupLink.
//
// Still unofficial: zca-js tracks Zalo's web client, so a Zalo release can move
// any of these. A wrong path fails LOUDLY with the server's error_code rather
// than corrupting anything, so the fix is one constant.
const (
	// pathGroupCreate creates a group. VERIFIED against zca-js createGroup.
	pathGroupCreate = "/api/group/create/v2"
	// pathGroupInvite adds members. VERIFIED against zca-js addUserToGroup.
	pathGroupInvite = "/api/group/invite/v2"
	// pathGroupKickout removes members. VERIFIED against zca-js removeUserFromGroup.
	pathGroupKickout = "/api/group/kickout"
	// pathGroupLink mints a group's join link.
	//
	// The original reconstruction guessed "/api/group/link/create" as a POST.
	// zca-js enableGroupLink uses "/api/group/link/new" as a GET — both the path
	// and the method were wrong, which is why this one is fetched through
	// getGroupAPI rather than callGroupAPI.
	pathGroupLink = "/api/group/link/new"
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
	// Field names follow zca-js createGroup exactly. Note the asymmetry with
	// AddGroupMembers below: create sends "membersTypes" (plural members),
	// invite sends "memberTypes" (singular). That is Zalo's own inconsistency,
	// not a typo here — normalising either one breaks that call.
	payload := map[string]any{
		"clientId":     time.Now().UnixMilli(),
		"gname":        name,
		"gdesc":        nil,
		"members":      memberIDs,
		"membersTypes": memberTypes(len(memberIDs)),
		"nameChanged":  1,
		"createLink":   createLink,
		"clientLang":   sess.Language,
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

	// "memberTypes", NOT "membersTypes" — the invite endpoint spells it
	// singular while create spells it plural. Zalo's inconsistency; matching
	// zca-js addUserToGroup verbatim is the only safe move. The original
	// reconstruction used the plural here and would have been rejected.
	payload := map[string]any{
		"grid":        groupID,
		"members":     memberIDs,
		"memberTypes": memberTypes(len(memberIDs)),
		"imei":        sess.IMEI,
		"clientLang":  sess.Language,
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

	// GET, not POST — this endpoint carries its encrypted params in the query
	// string. Sending it as a form POST (what the first cut did) reaches the
	// server with no params at all.
	data, err := getGroupAPI(ctx, sess, pathGroupLink, payload)
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

	return readEncryptedEnvelope(sess, resp, apiPath)
}

// readEncryptedEnvelope unwraps the OUTER envelope, then decrypts the data
// field (which holds a second envelope — decryptDataField handles that).
//
// Shared by the POST and GET helpers so the error shape is identical whichever
// verb an endpoint uses.
func readEncryptedEnvelope(sess *Session, resp *http.Response, apiPath string) (json.RawMessage, error) {
	var envelope Response[*string]
	if err := readJSON(resp, &envelope); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if envelope.ErrorCode != 0 {
		// A stale path lands here — surface the code and message verbatim so the
		// constant can be corrected without a capture session.
		return nil, fmt.Errorf("error code %d: %s (path %s)",
			envelope.ErrorCode, envelope.ErrorMessage, apiPath)
	}
	if envelope.Data == nil {
		return nil, fmt.Errorf("empty response data (path %s)", apiPath)
	}
	return decryptDataField(sess, *envelope.Data)
}

// getGroupAPI is callGroupAPI's GET counterpart: same encryption and same
// double-envelope decrypt, but the encrypted blob rides in the query string
// instead of a form body.
//
// Both shapes exist in this protocol and are not interchangeable — the group
// service rejects a GET endpoint called as a form POST by simply not seeing the
// params. FetchFriends uses this shape too.
func getGroupAPI(ctx context.Context, sess *Session, apiPath string, payload map[string]any) (json.RawMessage, error) {
	baseURL := getServiceURL(sess, "group")
	if baseURL == "" {
		return nil, fmt.Errorf("zalo_personal: no group service URL")
	}

	encData, err := encryptPayload(sess, payload)
	if err != nil {
		return nil, fmt.Errorf("encrypt payload: %w", err)
	}

	reqURL := makeURL(sess, baseURL+apiPath, map[string]any{"params": encData}, true)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	setDefaultHeaders(req, sess)

	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return readEncryptedEnvelope(sess, resp, apiPath)
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
