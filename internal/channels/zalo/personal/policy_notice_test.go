package personal

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/config"
)

// A personal Zalo account is a human's contact list, not a bot endpoint.
// Strangers arrive by design: the firm sends a contact request, the customer
// accepts and writes. On 2026-08-02 one of them got this as the firm's first
// words:
//
//	GoClaw: access not configured.
//	Your Zalo ID: 7362481190098513798
//	Pairing code: Z52Z5YKE
//	Ask the bot owner to approve with: goclaw pairing approve Z52Z5YKE
//
// The default has to be silence. An instance provisioned with no config at all
// — which is how zalo-pws was created — must not broadcast a pairing code.
func TestPairingNoticeDefaultsOffOnAPersonalAccount(t *testing.T) {
	c := &Channel{config: config.ZaloPersonalConfig{}}
	if c.pairingNoticeEnabled() {
		t.Fatal("an unconfigured personal-Zalo instance would send the operator pairing text " +
			"to whoever writes to it — including customers the firm invited")
	}

	off := false
	c = &Channel{config: config.ZaloPersonalConfig{PairingNotice: &off}}
	if c.pairingNoticeEnabled() {
		t.Error("explicit false must stay off")
	}
}

// A private account used only by staff can still opt in — the text is written
// for an operator and on that account the reader IS one.
func TestPairingNoticeIsOptIn(t *testing.T) {
	on := true
	c := &Channel{config: config.ZaloPersonalConfig{PairingNotice: &on}}
	if !c.pairingNoticeEnabled() {
		t.Fatal("pairing_notice=true must restore the in-chat notice")
	}
}
