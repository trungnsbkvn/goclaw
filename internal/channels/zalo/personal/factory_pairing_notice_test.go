package personal

import (
	"encoding/json"
	"testing"
)

// zaloInstanceConfig is the ONLY thing read out of a DB instance's JSON config,
// and every production instance is DB-backed. A field missing from that struct
// is dropped in silence — json.Unmarshal does not complain about keys it has
// nowhere to put — so the operator sets pairing_notice, sees no error, and
// nothing changes.
//
// This is exactly how PairingNotice shipped broken: added to
// config.ZaloPersonalConfig (which the config-FILE path uses) but not to the
// instance struct, which meant the documented opt-in did not exist on any real
// deployment.
func TestInstanceConfigCarriesPairingNotice(t *testing.T) {
	var ic zaloInstanceConfig
	raw := []byte(`{"dm_policy":"open","group_policy":"pairing","pairing_notice":true}`)
	if err := json.Unmarshal(raw, &ic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ic.PairingNotice == nil {
		t.Fatal("pairing_notice was dropped — the opt-in does not reach a DB-backed instance")
	}
	if !*ic.PairingNotice {
		t.Fatalf("pairing_notice = %v, want true", *ic.PairingNotice)
	}
	if ic.DMPolicy != "open" {
		t.Errorf("dm_policy = %q", ic.DMPolicy)
	}
}

// Absent stays nil, which is what makes the default silent.
func TestInstanceConfigPairingNoticeDefaultsNil(t *testing.T) {
	var ic zaloInstanceConfig
	if err := json.Unmarshal([]byte(`{"dm_policy":"open"}`), &ic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ic.PairingNotice != nil {
		t.Fatal("an unset pairing_notice must stay nil so the channel defaults to silent")
	}
}
