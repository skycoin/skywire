//go:build js

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
// SKYDEPLOY env-override has no analog here: browsers don't expose
// process env vars, and the install-page WASM ships its own
// deployment-default bundle. Operators who need a custom deployment
// edit services-config.json and rebuild (then re-run
// `go generate ./deployment/` to refresh data_static_js.go).
package deployment

import (
	"log"

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
}
