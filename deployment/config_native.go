//go:build !js

// Package deployment pkg/deployment/config_native.go
//
// Native (non-WASM) init for the deployment vars. Unmarshals the
// embedded services-config.json (or an external file pointed at by
// SKYDEPLOY) into Prod / Test / ProdConf / TestConf via
// encoding/json. The js/wasm build path bypasses this entirely —
// see config_js.go and data_static_js.go.
package deployment

import (
	"encoding/json"
	"log"
	"os"
)

// EnvServices is the wrapper struct for the outer JSON — i.e. 'prod'
// or 'test' deployment config. Lives in the !js file because its
// json.RawMessage fields require the encoding/json import.
type EnvServices struct {
	Test json.RawMessage `json:"test"`
	Prod json.RawMessage `json:"prod"`
}

func init() {
	// SKYDEPLOY overrides the embedded deployment config with a
	// user-supplied file. Supports private networks, corporate
	// deployments, and test environments.
	if path := os.Getenv("SKYDEPLOY"); path != "" {
		data, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			log.Panicf("SKYDEPLOY=%s: %v", path, err) //nolint:gosec
		}
		ServicesJSON = data
	}

	var envServices EnvServices
	err := json.Unmarshal(ServicesJSON, &envServices)
	if err != nil {
		log.Panic("services-config.json: ", err)
	}
	if envServices.Prod != nil {
		if err = json.Unmarshal(envServices.Prod, &Prod); err != nil {
			log.Panic(err)
		}
		if err = json.Unmarshal(envServices.Prod, &ProdConf); err != nil {
			log.Panic(err)
		}
	}
	if envServices.Test != nil {
		if err = json.Unmarshal(envServices.Test, &Test); err != nil {
			log.Panic(err)
		}
		if err = json.Unmarshal(envServices.Test, &TestConf); err != nil {
			log.Panic(err)
		}
	}
}
