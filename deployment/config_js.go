//go:build tinygo || js

// Package deployment pkg/deployment/config_js.go
//
// js/wasm init for the deployment vars. Populates Prod / Test /
// ProdConf / TestConf from the code-genned static literals in
// data_static_js.go instead of unmarshalling services-config.json
// via encoding/json — TinyGo 0.41.1's stdlib doesn't ship the
// reflect runtime helpers (reflect.unsafe_New, reflect.mapassign,
// etc.) that encoding/json needs, so dragging it into the install-
// page WASM build was blocking `tinygo build -target wasm`.
//
// SKYDEPLOY is honored when a command is given it in its environment, as
// natively. A browser visor started by autoconfig loads the SKYDEPLOY of
// its skywire.conf instead (cmd/skywire/commands/autoconfig_exec_js.go).
package deployment

import (
	"log"
	"os"

	"github.com/skycoin/skywire/pkg/cipher"
)

// mustPubKey parses a 33-byte hex-encoded public key. Called from
// data_static_js.go to construct compile-time PK literals; any
// parse failure means the gen'd file drifted from cipher.PubKey's
// wire format and warrants a regenerate, so panicking is correct.
func mustPubKey(s string) cipher.PubKey {
	var pk cipher.PubKey
	if err := pk.Set(s); err != nil {
		log.Panicf("deployment: bad PubKey literal %q: %v", s, err)
	}
	return pk
}

func init() {
	Prod = prodData
	Test = testData
	ProdConf = prodConfData
	TestConf = testConfData
	// services-config.json is dmsg-only (no plain-HTTP deployment URLs except
	// geoip); backfill the HTTP-named fields from their dmsg:// siblings. Mirrors
	// config_native.go. See Services.BackfillClearnetFromDmsg.
	Prod.BackfillClearnetFromDmsg()
	Test.BackfillClearnetFromDmsg()
	if ProdConf.Conf == "" {
		ProdConf.Conf = Prod.ConfDmsg
	}
	if TestConf.Conf == "" {
		TestConf.Conf = Test.ConfDmsg
	}
	// SKYDEPLOY works as it does natively for a command given it in its
	// environment. A bad file is logged, not fatal, so the desk still boots.
	if path := os.Getenv("SKYDEPLOY"); path != "" {
		if err := LoadFile(path); err != nil {
			log.Printf("SKYDEPLOY=%s: %v", path, err)
		}
	}
}
