//go:build !tinygo

// Package visorconfig pkg/visor/visorconfig/services_native.go c3-vis-core
//
// Native (non-WASM) home of the EnvServices alias. Lives here (rather
// than in services.go) because deployment.EnvServices is itself only
// defined under !js — its json.RawMessage fields need encoding/json,
// which is build-tag-gated off the WASM path. See services.go's
// package doc for context.
//
// This file used to also carry Fetch(), a plain-net/http helper that
// pulled a Services payload from the conf-service URL. It was removed:
// the deployment's conf service is dmsg-only (deployment.ProdConf.Conf
// is a dmsg:// URL), and a bare http.Client can never dial one — it
// fails with `unsupported protocol scheme "dmsg"`. Standing a dmsg
// client up here is not possible either: the CLI's dmsg fetch chain
// lives in cmd/skywire-cli, which imports pkg/visor, which imports this
// package, so the dependency can only run in that direction. Its sole
// caller (`skywire cli config update -a`) now uses the same
// dmsg-first fetch that `config gen` uses.
package visorconfig

import (
	"github.com/skycoin/skywire/deployment"
)

// EnvServices is the wrapper struct for the outer JSON. Aliased to
// deployment.EnvServices (only defined under !js — see deployment's
// config.go package doc for the WASM-stripping rationale). Consumers
// using visorconfig.EnvServices keep working under non-WASM builds.
type EnvServices = deployment.EnvServices
