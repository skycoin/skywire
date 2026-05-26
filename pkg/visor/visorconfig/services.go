// Package visorconfig pkg/visor/visorconfig/services.go
//
// Services + EnvServices are TYPE ALIASES to the deployment package
// rather than independent struct definitions. Reason: the visorconfig
// version was bit-identical to deployment.Services (same fields, same
// json tags) but lived in two places, which forced a redundant
// json.Unmarshal at every MakeBaseConfig call to convert the embedded
// deployment.ServicesJSON into a visorconfig.Services. The alias
// makes deployment.Prod / deployment.Test usable in-place — same
// memory, same fields, zero conversion — so visorconfig no longer
// needs to invoke encoding/json on the WASM-clean code path.
//
// HasDmsgEndpoints stays here as a method on the aliased type
// (works on alias receivers under Go's type-identity rules).
package visorconfig

import (
	"github.com/skycoin/skywire/deployment"
)

// EnvServices is the wrapper struct for the outer JSON (prod / test
// keys). Aliased to deployment.EnvServices so consumers keep working
// while only one copy of the type exists.
//
// The alias lives in services_native.go (//go:build !js) because
// deployment.EnvServices itself is only defined under !js — its
// json.RawMessage fields need encoding/json, which is build-tag-
// gated off the WASM path.

// Services is subdomains and IP addresses of the skywire services
// as deployed. Aliased to deployment.Services — both packages used
// to declare an identical struct, which created two json.Unmarshal
// hot paths (one in deployment.init, one in visorconfig.MakeBaseConfig)
// for the same data shape. The alias collapses the duplication.
type Services = deployment.Services

// HasDmsgEndpoints returns true if the services config has DMSG endpoints.
// Defined on the alias receiver here (rather than re-declared as a
// method) so callers that hold a *visorconfig.Services keep working.
// Note: deployment.Services already has a HasDmsgEndpoints method
// with the same semantics — Go's type-identity rule makes them the
// same method set, so this re-declaration would conflict and is
// omitted. Callers should use deployment.Services.HasDmsgEndpoints
// directly via the aliased type.
