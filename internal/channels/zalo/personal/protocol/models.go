package protocol

import (
	"encoding/base64"
	"encoding/json"
	"sort"
)

// SecretKey is a base64-encoded secret key from Zalo login.
type SecretKey string

func (s SecretKey) Bytes() []byte {
	decoded, err := base64.StdEncoding.DecodeString(string(s))
	if err != nil {
		return nil
	}
	return decoded
}

// LoginInfo from getLoginInfo response (AES-CBC encrypted).
type LoginInfo struct {
	UID             string          `json:"uid"`
	ZPWEnk          string          `json:"zpw_enk"`
	ZpwWebsocket    []string        `json:"zpw_ws"`
	ZpwServiceMapV3 ZpwServiceMapV3 `json:"zpw_service_map_v3"`
}

// ZpwServiceMapV3 holds Zalo service endpoint URLs.
//
// The named fields are the services GoClaw calls today. Raw keeps EVERYTHING
// the server advertised, including keys this struct does not name.
//
// Why Raw matters: this map is server-driven and undocumented, and the set of
// keys is not stable across Zalo web releases. Before Raw existed the five
// named fields were the only thing retained, so the answer to "does this
// account's session expose a `friend` service?" was unknowable without editing
// the struct, rebuilding, and re-logging-in — which is exactly the wrong loop
// for a reverse-engineered protocol. getServiceURL now falls back to Raw, so a
// service Zalo advertises is reachable whether or not it has a field here.
type ZpwServiceMapV3 struct {
	Chat      []string `json:"chat"`
	Group     []string `json:"group"`
	File      []string `json:"file"`
	Profile   []string `json:"profile"`
	GroupPoll []string `json:"group_poll"`

	// Raw is every advertised key → endpoints, populated by UnmarshalJSON.
	// Not a JSON field of its own (`-`): it is a second view of the same object.
	Raw map[string][]string `json:"-"`
}

// UnmarshalJSON parses the named fields, then keeps a raw copy of every key.
//
// Deliberately lenient: a malformed or unexpectedly-shaped entry is skipped
// rather than failed. This runs inside the login path, and an unfamiliar value
// in a service GoClaw never calls must not be able to break authentication.
func (m *ZpwServiceMapV3) UnmarshalJSON(data []byte) error {
	// alias drops the method set, so this does not recurse.
	type alias ZpwServiceMapV3
	var named alias
	if err := json.Unmarshal(data, &named); err != nil {
		return err
	}
	*m = ZpwServiceMapV3(named)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		// The named fields already parsed; an odd envelope must not fail login.
		return nil
	}
	m.Raw = make(map[string][]string, len(raw))
	for key, val := range raw {
		var list []string
		if err := json.Unmarshal(val, &list); err == nil {
			m.Raw[key] = list
			continue
		}
		// Some keys are a bare string rather than an array.
		var one string
		if err := json.Unmarshal(val, &one); err == nil && one != "" {
			m.Raw[key] = []string{one}
		}
	}
	return nil
}

// ServiceNames returns every service key the server advertised, sorted.
// Used by the diagnostic gateway method and by getServiceURL's warning, so an
// unreachable service reports what WAS on offer instead of just "not found".
func (m ZpwServiceMapV3) ServiceNames() []string {
	names := make([]string, 0, len(m.Raw))
	for key := range m.Raw {
		names = append(names, key)
	}
	sort.Strings(names)
	return names
}

// ServerInfo from getServerInfo response.
type ServerInfo struct {
	Settings *Settings `json:"settings"`
}

// UnmarshalJSON handles Zalo's typo: "setttings" (3 t's) vs "settings".
func (s *ServerInfo) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, k := range []string{"settings", "setttings"} {
		if v, ok := raw[k]; ok {
			return json.Unmarshal(v, &s.Settings)
		}
	}
	return nil
}

// Settings holds server-provided configuration.
type Settings struct {
	Features  Features          `json:"features"`
	Keepalive KeepaliveSettings `json:"keepalive"`
}

type Features struct {
	Socket SocketSettings `json:"socket"`
}

type SocketSettings struct {
	PingInterval     int                          `json:"ping_interval"`
	Retries          map[string]SocketRetryConfig `json:"retries"`
	CloseAndRetry    []int                        `json:"close_and_retry_codes"`
	RotateErrorCodes []int                        `json:"rotate_error_codes"`
}

type SocketRetryConfig struct {
	Max   *int  `json:"max,omitempty"`
	Times []int `json:"times"`
}

// UnmarshalJSON handles Zalo's OneOrMany pattern: value can be int or []int.
func (r *SocketRetryConfig) UnmarshalJSON(data []byte) error {
	type Alias struct {
		Max   *int            `json:"max,omitempty"`
		Times json.RawMessage `json:"times"`
	}
	var a Alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	r.Max = a.Max
	// Try []int first
	if err := json.Unmarshal(a.Times, &r.Times); err != nil {
		// Try single int
		var single int
		if err2 := json.Unmarshal(a.Times, &single); err2 != nil {
			return err
		}
		r.Times = []int{single}
	}
	return nil
}

type KeepaliveSettings struct {
	AlwaysKeepalive   uint `json:"alway_keepalive"`
	KeepaliveDuration uint `json:"keepalive_duration"`
}

// --- Zalo API response types ---

// Response is the generic Zalo API response envelope.
type Response[T any] struct {
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
	Data         T      `json:"data"`
}

// QRGeneratedData from QR code generation response.
type QRGeneratedData struct {
	Code  string `json:"code"`
	Image string `json:"image"` // base64-encoded PNG with data URI prefix
}

// QRScannedData from QR waiting-scan response.
type QRScannedData struct {
	Avatar      string `json:"avatar"`
	DisplayName string `json:"display_name"`
}

// QRUserInfo from getUserInfo response.
type QRUserInfo struct {
	Logged bool     `json:"logged"`
	Info   UserInfo `json:"info"`
}

// UserInfo holds basic Zalo user info.
type UserInfo struct {
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}
