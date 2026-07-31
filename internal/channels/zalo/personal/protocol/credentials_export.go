package protocol

import (
	"net/http"
	"net/url"
)

// Credential refresh.
//
// Zalo rotates session cookies (`zpw_sek` and friends) via Set-Cookie during
// normal operation. Those land in the live `Session.CookieJar` — and until this
// file existed, they were never written back anywhere. The only persist in the
// whole Zalo tree happened once, at QR time.
//
// The consequence was quiet but fatal: after any process restart goclaw replayed
// the ORIGINAL cookie set captured when the QR was first scanned. The lifetime
// of the whole integration was therefore bounded by the lifetime of that first
// cookie set, with no mechanism to extend it short of re-scanning the QR — and
// when it finally lapsed mid-session the channel died silently (see listen.go's
// give-up path).
//
// ExportCredentials snapshots the CURRENT jar so the caller can persist a
// refreshed set, turning cookie rotation from a slow leak into a self-healing
// property.

// credentialHosts are the hosts whose cookies matter for a re-login.
//
// Go's cookiejar is queried per-URL and cannot enumerate itself, so the set has
// to be explicit. These mirror what seedServiceMapCookies populates: the base
// chat host plus the login host. Service-map hosts are added by the caller from
// the live LoginInfo, because they are assigned per-account.
var credentialHosts = []url.URL{
	DefaultBaseURL,
	{Scheme: "https", Host: "wpa.chat.zalo.me", Path: "/"},
}

// ExportCredentials snapshots the session's current cookies into a Credentials
// value suitable for persisting and later replaying through
// LoginWithCredentials.
//
// Returns nil when the session is not usable as a credential source (no jar, no
// IMEI), so a caller can persist unconditionally without guarding.
func ExportCredentials(sess *Session) *Credentials {
	if sess == nil || sess.CookieJar == nil || sess.IMEI == "" {
		return nil
	}

	// Collect across every host we know about, de-duped by (name, domain) so a
	// cookie present on several hosts is stored once. Later hosts win, which is
	// what we want: the service-map hosts are appended last and carry the
	// freshest values for an active session.
	seen := make(map[string]int)
	var merged []*http.Cookie

	appendFrom := func(u url.URL) {
		for _, c := range sess.CookieJar.Cookies(&u) {
			if c == nil || c.Name == "" {
				continue
			}
			key := c.Name + "\x00" + c.Domain
			if idx, dup := seen[key]; dup {
				merged[idx] = c
				continue
			}
			seen[key] = len(merged)
			merged = append(merged, c)
		}
	}

	for _, u := range credentialHosts {
		appendFrom(u)
	}
	for _, raw := range serviceMapHosts(sess) {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			appendFrom(url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"})
		}
	}

	if len(merged) == 0 {
		// An empty jar would produce credentials that fail on replay AND
		// overwrite a good saved set. Refuse rather than persist a dud.
		return nil
	}

	cookies := NewHTTPCookies(merged)
	cred := &Credentials{
		IMEI:      sess.IMEI,
		Cookie:    &cookies,
		UserAgent: sess.UserAgent,
	}
	if sess.Language != "" {
		lang := sess.Language
		cred.Language = &lang
	}
	if !cred.IsValid() {
		return nil
	}
	return cred
}

// serviceMapHosts lists every distinct service URL the account was assigned.
// Mirrors seedServiceMapCookies' source so export and seed stay symmetric.
func serviceMapHosts(sess *Session) []string {
	if sess == nil || sess.LoginInfo == nil {
		return nil
	}
	m := sess.LoginInfo.ZpwServiceMapV3
	var out []string
	for _, group := range [][]string{m.Chat, m.Group, m.File, m.Profile, m.GroupPoll} {
		out = append(out, group...)
	}
	return out
}
