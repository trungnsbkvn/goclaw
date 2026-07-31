package protocol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// friendSession points the "friend" service at a test server. Note it goes
// through the RAW advertised map rather than a named struct field — that is the
// whole reason these calls became reachable, so the test exercises that path.
func friendSession(t *testing.T, srv *httptest.Server) *Session {
	t.Helper()
	sess := newTestSession(t, srv)
	sess.LoginInfo.ZpwServiceMapV3.Raw = map[string][]string{"friend": {srv.URL}}
	return sess
}

func TestFindUserByPhoneResolvesProfile(t *testing.T) {
	var gotPath, gotMethod string
	var gotPayload map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotPayload = decodeQueryParams(t, r)
		_, _ = w.Write([]byte(encodeReply(t, map[string]any{
			"uid": "zalo-uid-42", "display_name": "Nguyễn Văn A", "zalo_name": "vana",
		})))
	}))
	defer srv.Close()

	user, err := FindUserByPhone(context.Background(), friendSession(t, srv), "0816333868")
	if err != nil {
		t.Fatalf("FindUserByPhone: %v", err)
	}
	if gotPath != pathFindUser || gotMethod != http.MethodGet {
		t.Errorf("%s %s, want GET %s", gotMethod, gotPath, pathFindUser)
	}
	// The API wants the 84 form; sending "0816333868" finds nobody.
	if gotPayload["phone"] != "84816333868" {
		t.Errorf("phone = %v, want 84816333868", gotPayload["phone"])
	}
	if gotPayload["reqSrc"] != float64(reqSrcFindUser) {
		t.Errorf("reqSrc = %v, want %d", gotPayload["reqSrc"], reqSrcFindUser)
	}
	if user.UserID != "zalo-uid-42" || user.DisplayName != "Nguyễn Văn A" {
		t.Errorf("user = %+v", user)
	}
}

// A number that is not on Zalo — or an account that has turned off
// discovery-by-phone — is an ordinary outcome, not a transport failure, and
// callers must be able to tell the two apart.
func TestFindUserByPhoneNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error_code":216,"error_message":"user not found"}`))
	}))
	defer srv.Close()

	_, err := FindUserByPhone(context.Background(), friendSession(t, srv), "0816333868")
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("err = %v, want ErrUserNotFound", err)
	}
}

// A real transport/API error must NOT masquerade as "not found" — that would
// turn an outage into "this customer has no Zalo".
func TestFindUserByPhoneDistinguishesRealErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error_code":114,"error_message":"invalid session"}`))
	}))
	defer srv.Close()

	_, err := FindUserByPhone(context.Background(), friendSession(t, srv), "0816333868")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrUserNotFound) {
		t.Error("a session error must not read as ErrUserNotFound")
	}
}

// Without a friend service the failure must name that, not surface as an
// unexplained empty URL — the exact confusion the service-map widening removed.
func TestFriendCallsRequireTheFriendService(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	sess := newTestSession(t, srv) // group service only, no friend key
	if _, err := FindUserByPhone(context.Background(), sess, "0816333868"); err == nil {
		t.Error("must fail without a friend service")
	}
	if err := SendFriendRequest(context.Background(), sess, "uid", "hi"); err == nil {
		t.Error("must fail without a friend service")
	}
}

func TestSendFriendRequestPayload(t *testing.T) {
	var gotPath, gotMethod string
	var gotPayload map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotPayload = decodeRequestParams(t, r)
		_, _ = w.Write([]byte(encodeReply(t, map[string]any{})))
	}))
	defer srv.Close()

	err := SendFriendRequest(context.Background(), friendSession(t, srv), "uid-9", "Chào anh/chị")
	if err != nil {
		t.Fatalf("SendFriendRequest: %v", err)
	}
	if gotPath != pathSendFriendRequest || gotMethod != http.MethodPost {
		t.Errorf("%s %s, want POST %s", gotMethod, gotPath, pathSendFriendRequest)
	}
	if gotPayload["toid"] != "uid-9" || gotPayload["msg"] != "Chào anh/chị" {
		t.Errorf("payload = %+v", gotPayload)
	}
	// reqsrc is lowercase here and camelCase in findUser — Zalo's own spelling,
	// and the values differ too (30 vs 40). Not interchangeable.
	if gotPayload["reqsrc"] != float64(reqSrcFriendRequest) {
		t.Errorf("reqsrc = %v, want %d", gotPayload["reqsrc"], reqSrcFriendRequest)
	}
	// srcParams is a JSON STRING, not a nested object.
	if s, ok := gotPayload["srcParams"].(string); !ok || s != `{"uidTo":"uid-9"}` {
		t.Errorf("srcParams = %v, want the JSON string {\"uidTo\":\"uid-9\"}", gotPayload["srcParams"])
	}
}

func TestSendFriendRequestRejectsEmptyUser(t *testing.T) {
	if err := SendFriendRequest(context.Background(), &Session{}, "", "hi"); err == nil {
		t.Error("an empty user id must be rejected before dialling")
	}
}

func TestNormalizePhoneForZalo(t *testing.T) {
	tests := []struct{ in, lang, want string }{
		{"0816333868", "vi", "84816333868"},
		{"0816 333 868", "vi", "84816333868"},
		{"0816.333.868", "vi", "84816333868"},
		{"84816333868", "vi", "84816333868"},
		{"+84816333868", "vi", "84816333868"},
		// A leading 0 means different things in different numbering plans, so
		// zca-js only rewrites it for Vietnamese sessions. Blindly prefixing 84
		// would look up a real but WRONG number rather than failing.
		{"0816333868", "en", "0816333868"},
		{"", "vi", ""},
		{"   ", "vi", ""},
	}
	for _, tc := range tests {
		if got := normalizePhoneForZalo(tc.in, tc.lang); got != tc.want {
			t.Errorf("normalizePhoneForZalo(%q, %q) = %q, want %q", tc.in, tc.lang, got, tc.want)
		}
	}
}
