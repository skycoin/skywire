module skywire-routing-policy-bundle

go 1.26.4

// The preset decide/tick logic is the single source of truth in the main
// skywire module (pkg/router/policy/preset); this bundle is a thin wasm ABI
// shim over it. The replace points at the repo root so `tinygo build` compiles
// the in-tree package. preset imports only the Go stdlib, so no go.sum / external
// downloads are pulled into this module.
require github.com/skycoin/skywire v0.0.0

replace github.com/skycoin/skywire => ../../../../..
