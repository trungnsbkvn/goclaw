package protocol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The group API paths are reconstructed from the zca-js client rather than
// captured from a live session, so what these tests lock down is the PLUMBING —
// URL defaults, payload encryption, form encoding, and the double-envelope
// decrypt. That plumbing is shared with group messaging, so a regression here
// would break sending too. A wrong path is caught by the live call returning a
// non-zero error_code, which TestCallGroupAPISurfacesServerError covers.

const testAESKey = "0123456789ABCDEF" // 16 bytes = AES-128

// newTestSession wires a Session whose "group" service points at srv.
func newTestSession(t *testing.T, srv *httptest.Server) *Session {
	t.Helper()
	return &Session{
		UID:       "self-uid",
		IMEI:      "test-imei",
		UserAgent: DefaultUserAgent,
		Language:  DefaultLanguage,
		SecretKey: base64.StdEncoding.EncodeToString([]byte(testAESKey)),
		LoginInfo: &LoginInfo{
			UID: "self-uid",
			ZpwServiceMapV3: ZpwServiceMapV3{
				Group: []string{srv.URL},
			},
		},
		Client: srv.Client(),
	}
}

// encodeReply builds the response Zalo actually sends: an outer envelope whose
// data is the AES-encrypted form of an INNER envelope wrapping the payload.
func encodeReply(t *testing.T, payload any) string {
	t.Helper()
	inner, err := json.Marshal(map[string]any{
		"error_code": 0,
		"data":       payload,
	})
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	enc, err := EncodeAESCBC([]byte(testAESKey), string(inner), false)
	if err != nil {
		t.Fatalf("encrypt inner: %v", err)
	}
	body, err := json.Marshal(map[string]any{"error_code": 0, "data": enc})
	if err != nil {
		t.Fatalf("marshal outer: %v", err)
	}
	return string(body)
}

// decodeRequestParams reverses what callGroupAPI sent, so a test can assert on
// the actual decrypted payload rather than trusting the code that built it.
func decodeRequestParams(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Fatalf("parse form: %v", err)
	}
	raw := r.PostForm.Get("params")
	if raw == "" {
		t.Fatal("request carried no params field")
	}
	unescaped, err := url.PathUnescape(raw)
	if err != nil {
		t.Fatalf("unescape params: %v", err)
	}
	plain, err := DecodeAESCBC([]byte(testAESKey), unescaped)
	if err != nil {
		t.Fatalf("decrypt params: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(plain, &out); err != nil {
		t.Fatalf("parse params: %v", err)
	}
	return out
}

func TestCreateGroupSendsEncryptedPayload(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	var gotPayload map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		gotPayload = decodeRequestParams(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(encodeReply(t, map[string]any{
			"groupId":       "g-123",
			"groupType":     2,
			"sucessMembers": []string{"u1"},
			"errorMembers":  []string{"u2"},
			"group_link":    "https://zalo.me/g/abcdef",
		})))
	}))
	defer srv.Close()

	sess := newTestSession(t, srv)
	res, err := CreateGroup(context.Background(), sess, "Vụ Nguyễn Văn A", []string{"u1", "u2"}, true)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if gotPath != pathGroupCreate {
		t.Errorf("path = %q, want %q", gotPath, pathGroupCreate)
	}
	// Zalo rejects calls without these; makeURL adds them and a regression
	// there is otherwise invisible until production.
	if gotQuery.Get("zpw_ver") != "671" || gotQuery.Get("zpw_type") != "30" {
		t.Errorf("missing API version markers: %v", gotQuery)
	}
	if gotPayload["gname"] != "Vụ Nguyễn Văn A" {
		t.Errorf("gname = %v", gotPayload["gname"])
	}
	if gotPayload["createLink"] != float64(1) {
		t.Errorf("createLink = %v, want 1", gotPayload["createLink"])
	}
	// members and membersTypes are PARALLEL arrays; a length mismatch is
	// rejected by the server in a way that is hard to read.
	members, _ := gotPayload["members"].([]any)
	types, _ := gotPayload["membersTypes"].([]any)
	if len(members) != 2 || len(types) != 2 {
		t.Fatalf("members=%v membersTypes=%v, want 2 each", members, types)
	}
	for i, v := range types {
		if v != float64(memberTypeUser) {
			t.Errorf("membersTypes[%d] = %v, want %d", i, v, memberTypeUser)
		}
	}

	if res.GroupID != "g-123" {
		t.Errorf("GroupID = %q", res.GroupID)
	}
	if res.Link != "https://zalo.me/g/abcdef" {
		t.Errorf("Link = %q", res.Link)
	}
	// The partial-failure list must survive: a group created without the
	// customer in it is the failure mode this whole flow exists to avoid.
	if len(res.ErrorMembers) != 1 || res.ErrorMembers[0] != "u2" {
		t.Errorf("ErrorMembers = %v, want [u2]", res.ErrorMembers)
	}
}

// Zalo returns groupId as a number in some responses and a string in others.
func TestCreateGroupAcceptsNumericGroupID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(encodeReply(t, map[string]any{"groupId": 987654321})))
	}))
	defer srv.Close()

	res, err := CreateGroup(context.Background(), newTestSession(t, srv), "G", []string{"u1"}, false)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if res.GroupID != "987654321" {
		t.Errorf("GroupID = %q, want 987654321", res.GroupID)
	}
}

func TestCreateGroupRejectsEmptyInput(t *testing.T) {
	sess := &Session{}
	if _, err := CreateGroup(context.Background(), sess, "", []string{"u1"}, false); err == nil {
		t.Error("empty name must be rejected before any network call")
	}
	if _, err := CreateGroup(context.Background(), sess, "G", nil, false); err == nil {
		t.Error("empty member list must be rejected before any network call")
	}
}

func TestAddGroupMembersReportsPartialFailure(t *testing.T) {
	var gotPath string
	var gotPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotPayload = decodeRequestParams(t, r)
		_, _ = w.Write([]byte(encodeReply(t, map[string]any{"errorMembers": []string{"u9"}})))
	}))
	defer srv.Close()

	res, err := AddGroupMembers(context.Background(), newTestSession(t, srv), "g-1", []string{"u8", "u9"})
	if err != nil {
		t.Fatalf("AddGroupMembers: %v", err)
	}
	if gotPath != pathGroupInvite {
		t.Errorf("path = %q, want %q", gotPath, pathGroupInvite)
	}
	if gotPayload["grid"] != "g-1" {
		t.Errorf("grid = %v", gotPayload["grid"])
	}
	// A nil error here does NOT mean everyone joined — adding a non-friend is
	// commonly refused per-member. Callers must read ErrorMembers.
	if len(res.ErrorMembers) != 1 || res.ErrorMembers[0] != "u9" {
		t.Errorf("ErrorMembers = %v, want [u9]", res.ErrorMembers)
	}
}

// Full success can come back as an empty array rather than an object; that must
// read as "nobody failed", not as a parse error.
func TestAddGroupMembersHandlesArrayResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(encodeReply(t, []any{})))
	}))
	defer srv.Close()

	res, err := AddGroupMembers(context.Background(), newTestSession(t, srv), "g-1", []string{"u8"})
	if err != nil {
		t.Fatalf("AddGroupMembers: %v", err)
	}
	if len(res.ErrorMembers) != 0 {
		t.Errorf("ErrorMembers = %v, want empty", res.ErrorMembers)
	}
}

func TestRemoveGroupMembers(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(encodeReply(t, map[string]any{"errorMembers": []string{}})))
	}))
	defer srv.Close()

	if _, err := RemoveGroupMembers(context.Background(), newTestSession(t, srv), "g-1", []string{"u8"}); err != nil {
		t.Fatalf("RemoveGroupMembers: %v", err)
	}
	if gotPath != pathGroupKickout {
		t.Errorf("path = %q, want %q", gotPath, pathGroupKickout)
	}
}

// The link field name varies between responses, so all known spellings resolve.
func TestGroupInviteLinkAcceptsFieldAliases(t *testing.T) {
	for _, field := range []string{"link", "group_link", "url"} {
		t.Run(field, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(encodeReply(t, map[string]any{field: "https://zalo.me/g/xyz"})))
			}))
			defer srv.Close()

			link, err := GroupInviteLink(context.Background(), newTestSession(t, srv), "g-1")
			if err != nil {
				t.Fatalf("GroupInviteLink: %v", err)
			}
			if link != "https://zalo.me/g/xyz" {
				t.Errorf("link = %q", link)
			}
		})
	}
}

func TestGroupInviteLinkErrorsWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(encodeReply(t, map[string]any{"somethingElse": 1})))
	}))
	defer srv.Close()

	if _, err := GroupInviteLink(context.Background(), newTestSession(t, srv), "g-1"); err == nil {
		t.Error("a response with no link must fail loudly, not return empty string")
	}
}

// A wrong reconstructed path shows up as a non-zero error_code. The error must
// carry the code AND the path so the constant can be fixed from a log line.
func TestCallGroupAPISurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error_code":114,"error_message":"invalid params"}`))
	}))
	defer srv.Close()

	_, err := CreateGroup(context.Background(), newTestSession(t, srv), "G", []string{"u1"}, false)
	if err == nil {
		t.Fatal("expected an error for a non-zero error_code")
	}
	for _, want := range []string{"114", "invalid params", pathGroupCreate} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestCallGroupAPIRequiresGroupService(t *testing.T) {
	sess := &Session{LoginInfo: &LoginInfo{}} // no group endpoint advertised
	if _, err := AddGroupMembers(context.Background(), sess, "g-1", []string{"u1"}); err == nil {
		t.Error("a session with no group service must fail before dialling")
	}
}
