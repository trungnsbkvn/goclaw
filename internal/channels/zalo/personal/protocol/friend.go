package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Friend lookup and friend requests.
//
// These live on the "friend" service, which this struct did not name until the
// service map was widened to keep every advertised key. That is why they were
// deferred on the first pass: the service key was unknowable without a live
// session. It is now confirmed — zca-js resolves both from
// api.zpwServiceMap.friend[0], and getServiceURL reaches unnamed services
// through the raw advertised map.
//
// Paths and parameters match RFS-ADRENO/zca-js src/apis/{findUser,
// sendFriendRequest}.ts.
//
// ── READ THIS BEFORE ENABLING SendFriendRequest ─────────────────────────────
//
// FindUserByPhone is a READ and carries ordinary risk. SendFriendRequest does
// not: unsolicited friend requests to strangers are among the strongest
// automated-abuse signals Zalo acts on, and the account at stake is the firm's
// — losing it takes the contact list and every group that account owns.
//
// It also raises a question that is not technical. A phone number a customer
// gave for one purpose (an OA support conversation) being used to approach them
// from a personal account is a change of processing purpose under Nghị định
// 13/2023, and the customer consented to the OA, not to a personal account.
// Whether that is acceptable is a decision for the firm, not a default.
//
// So this ships available but unused: nothing in GoClaw calls it, and the
// jus-hub escalation path deliberately hands the customer an invite LINK
// instead, which the customer chooses to tap.

const (
	// pathFindUser resolves a phone number to a Zalo profile.
	pathFindUser = "/api/friend/profile/get"
	// pathSendFriendRequest sends a friend request to a uid.
	pathSendFriendRequest = "/api/friend/sendreq"
)

const (
	// reqSrcFindUser / reqSrcFriendRequest are the client-origin markers Zalo
	// web sends. Values from zca-js; they are not interchangeable.
	reqSrcFindUser       = 40
	reqSrcFriendRequest  = 30
	avatarSizeLargeValue = 240
)

// errCodeUserNotFound is what Zalo returns when a phone number matches no
// account. Treated as "not found" rather than a failure — a number that is not
// on Zalo is an ordinary outcome, not an error worth propagating.
const errCodeUserNotFound = 216

// ZaloUser is a profile resolved from a phone number.
type ZaloUser struct {
	UserID      string `json:"uid"`
	DisplayName string `json:"display_name"`
	ZaloName    string `json:"zalo_name"`
	Avatar      string `json:"avatar"`
	// Phone is echoed back only when the account has made it visible; it is
	// usually empty and must never be relied on.
	Phone string `json:"phoneNumber"`
}

// ErrUserNotFound — the phone number is not on Zalo, or the account is not
// discoverable by phone (a per-user privacy setting).
var ErrUserNotFound = fmt.Errorf("zalo_personal: no Zalo account for that number")

// FindUserByPhone resolves a Vietnamese phone number to a Zalo profile.
//
// This is the lookup that makes "customer messaged our OA, now reach them on
// the personal account" technically possible. Whether it SHOULD be used that
// way is the consent question in the file header — the capability existing does
// not settle it.
//
// Returns ErrUserNotFound when the number matches no discoverable account, so
// callers can distinguish that from a transport failure.
func FindUserByPhone(ctx context.Context, sess *Session, phone string) (*ZaloUser, error) {
	phone = normalizePhoneForZalo(phone, sess.Language)
	if phone == "" {
		return nil, fmt.Errorf("zalo_personal: empty phone number")
	}

	baseURL := getServiceURL(sess, "friend")
	if baseURL == "" {
		// The widened service map means this is now a real answer rather than
		// an unnamed-key artefact: the account was not offered a friend service.
		return nil, fmt.Errorf("zalo_personal: this session has no friend service")
	}

	payload := map[string]any{
		"phone":       phone,
		"avatar_size": avatarSizeLargeValue,
		"language":    sess.Language,
		"imei":        sess.IMEI,
		"reqSrc":      reqSrcFindUser,
	}
	encData, err := encryptPayload(sess, payload)
	if err != nil {
		return nil, fmt.Errorf("zalo_personal: encrypt find-user payload: %w", err)
	}

	reqURL := makeURL(sess, baseURL+pathFindUser, map[string]any{"params": encData}, true)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	setDefaultHeaders(req, sess)

	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zalo_personal: find user: %w", err)
	}
	defer resp.Body.Close()

	var envelope Response[*string]
	if err := readJSON(resp, &envelope); err != nil {
		return nil, fmt.Errorf("zalo_personal: parse find-user response: %w", err)
	}
	if envelope.ErrorCode == errCodeUserNotFound {
		return nil, ErrUserNotFound
	}
	if envelope.ErrorCode != 0 {
		return nil, fmt.Errorf("zalo_personal: find user error code %d: %s",
			envelope.ErrorCode, envelope.ErrorMessage)
	}
	if envelope.Data == nil {
		return nil, ErrUserNotFound
	}

	plain, err := decryptDataField(sess, *envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("zalo_personal: decrypt find-user: %w", err)
	}
	var user ZaloUser
	if err := json.Unmarshal(plain, &user); err != nil {
		return nil, fmt.Errorf("zalo_personal: parse profile: %w", err)
	}
	if user.UserID == "" {
		return nil, ErrUserNotFound
	}
	return &user, nil
}

// SendFriendRequest sends a friend request to a uid.
//
// Nothing in GoClaw calls this. See the file header before wiring it to
// anything automated: this is the single highest-ban-risk call in the package,
// and using it on numbers harvested from another channel is a consent question
// as much as a technical one.
func SendFriendRequest(ctx context.Context, sess *Session, userID, message string) error {
	if userID == "" {
		return fmt.Errorf("zalo_personal: friend request needs a user id")
	}

	baseURL := getServiceURL(sess, "friend")
	if baseURL == "" {
		return fmt.Errorf("zalo_personal: this session has no friend service")
	}

	srcParams, err := json.Marshal(map[string]string{"uidTo": userID})
	if err != nil {
		return err
	}
	payload := map[string]any{
		"toid":      userID,
		"msg":       message,
		"reqsrc":    reqSrcFriendRequest,
		"imei":      sess.IMEI,
		"language":  sess.Language,
		"srcParams": string(srcParams),
	}
	encData, err := encryptPayload(sess, payload)
	if err != nil {
		return fmt.Errorf("zalo_personal: encrypt friend-request payload: %w", err)
	}

	reqURL := makeURL(sess, baseURL+pathSendFriendRequest, nil, true)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL,
		buildFormBody(map[string]string{"params": encData}))
	if err != nil {
		return err
	}
	setDefaultHeaders(req, sess)

	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("zalo_personal: send friend request: %w", err)
	}
	defer resp.Body.Close()

	var envelope Response[*string]
	if err := readJSON(resp, &envelope); err != nil {
		return fmt.Errorf("zalo_personal: parse friend-request response: %w", err)
	}
	if envelope.ErrorCode != 0 {
		return fmt.Errorf("zalo_personal: friend request error code %d: %s",
			envelope.ErrorCode, envelope.ErrorMessage)
	}
	return nil
}

// normalizePhoneForZalo converts a local Vietnamese number to the 84-prefixed
// form the API expects, mirroring zca-js findUser.
//
// Only rewrites a leading 0 when the session language is Vietnamese — the same
// condition zca-js applies, because a leading 0 means different things in
// different national numbering plans and blindly prefixing 84 would produce a
// wrong number rather than a failed lookup.
func normalizePhoneForZalo(phone, language string) string {
	phone = strings.TrimSpace(phone)
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if digits == "" {
		return ""
	}
	if strings.HasPrefix(digits, "0") && language == "vi" {
		return "84" + digits[1:]
	}
	return digits
}
