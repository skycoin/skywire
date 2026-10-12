//go:build !(tinygo || js)

// Package deployment pkg/deployment/config_native.go
//
// Native (non-WASM) init for the deployment vars. Unmarshals the
// embedded services-config.json (or an external file pointed at by
// SKYDEPLOY) into Prod / Test / ProdConf / TestConf via
// encoding/json. The js/wasm build starts from the static literals
// instead; see config_js.go and data_static_js.go.
package deployment

import (
	"log"
	"os"
)

func init() {
	// SKYDEPLOY overrides the embedded deployment config with a
	// user-supplied file. Supports private networks, corporate
	// deployments, and test environments.
	if path := os.Getenv("SKYDEPLOY"); path != "" {
		if err := LoadFile(path); err != nil {
			log.Panicf("SKYDEPLOY=%s: %v", path, err) //nolint:gosec
		}
		return
	}
	if err := applyServicesJSON(ServicesJSON); err != nil {
		log.Panic("services-config.json: ", err)
	}
}
