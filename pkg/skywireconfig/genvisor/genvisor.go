// Package genvisor provides Generate — a single entry point that
// composes a fresh visor configuration (pkg/visor/visorconfig.V1)
// from a small Options struct plus the deployment-default services
// embedded in pkg/skycoin/skywire/deployment (deployment.Prod /
// deployment.Test).
//
// Lives in pkg/skywireconfig so it sits alongside autoconfigcmd
// and keypair as the third leg of the WASM-clean configuration
// surface: the apt-repo install-page form generates a downloadable
// /opt/skywire/skywire.json by calling Generate, encoding the
// returned V1 as JSON, and offering it as a Blob the operator
// downloads.
//
// Generate is a thin shim around the visorconfig package's
// MakeBaseConfig helper (the same function cmd/skywire-cli's
// `config gen` builds on) — semantics are identical between the
// CLI's config-gen path and this WASM-compatible path. The Options
// struct exposes the most commonly-tuned knobs as fields so a
// WASM caller doesn't have to compose a full cobra command flag
// set just to set ISHYPERVISOR or REWARDSKYADDR; everything else
// can be edited on the returned V1 directly before JSON-encoding.
//
// WASM cleanliness: this package imports only WASM-clean leaves
// (visorconfig, deployment, cipher, skyenv-spec). It compiles
// under GOOS=js GOARCH=wasm — verified end-to-end by the
// apt-repo install page's WASM build.
package genvisor

import (
	"errors"
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Options groups the operator-tunable knobs Generate honors.
// Every field is optional — zero values mean "use the visorconfig
// default for that field". Anything not exposed here (advanced
// transport setup tweaks, multi-deployment dmsg, etc.) is still
// reachable on the returned *visorconfig.V1 — Generate gives you
// a well-formed config; further mutation is the caller's call.
type Options struct {
	// SecretKey, when non-zero, becomes the visor's identity. The
	// matching PK is derived and written into Common.PK. Empty
	// triggers a fresh keypair via cipher.GenerateKeyPair (the
	// same path visorconfig.NewCommon takes when sk==nil but a
	// SecKey pointer is passed; see ensureKeys).
	SecretKey cipher.SecKey

	// IsHypervisor enables the local hypervisor stanza
	// (V1.Hypervisor.Enable=true and the rest of HypervisorConfig
	// gets populated by visorconfig.GenerateWorkDirConfig).
	IsHypervisor bool

	// HypervisorPKs is a list of remote hypervisor public keys to
	// dial. The visor opens a persistent RPC channel to each on
	// start. Empty leaves V1.Hypervisors nil.
	HypervisorPKs []cipher.PubKey

	// RewardAddress, when non-empty, is written verbatim to
	// V1.RewardAddress. Validated only at the wire level (must be
	// a Skycoin address or xpub string); the visor performs its
	// own deeper validation on first start.
	RewardAddress string

	// IsPublic toggles V1.IsPublic — when true, the visor
	// announces itself to the service-discovery service so other
	// operators can dial it as a transport endpoint.
	IsPublic bool

	// TestEnv selects the test deployment instead of prod. Affects
	// every service URL Generate composes (dmsg-discovery,
	// transport-discovery, route-finder, etc.). Mirrors the
	// `--testenv` flag in cli config gen.
	TestEnv bool
}

// Generate composes a fresh visorconfig.V1 from opts + the
// embedded deployment configuration. Returns the V1 and any error
// from key derivation or services unmarshaling.
//
// The returned V1 is operationally complete enough to load into a
// fresh visor: all required fields are populated with deployment
// defaults, the operator's identity is wired into Common, and the
// flags from Options that translate to V1 fields are applied.
// Optional sub-systems (Stats, Skychat, SkymailBridge, UI server,
// log server, etc.) are left at visorconfig's defaults.
//
// The same set of fields can be tuned post-call by mutating the
// returned V1 directly — Generate is the prelude to "marshal +
// download" in the WASM flow.
func Generate(opts Options) (*visorconfig.V1, error) {
	sk := opts.SecretKey
	common, err := visorconfig.NewCommon(nil, "", &sk)
	if err != nil {
		return nil, fmt.Errorf("genvisor: derive identity: %w", err)
	}

	// MakeBaseConfig unmarshals deployment.ServicesJSON internally
	// when services==nil. We pass nil so the embedded Prod (or
	// Test) deployment is used — same path the visor takes on
	// first boot when the conf-service is unreachable.
	conf := visorconfig.MakeBaseConfig(common, opts.TestEnv, false, nil, nil)
	if conf == nil {
		return nil, errors.New("genvisor: MakeBaseConfig returned nil (services unmarshal failed)")
	}

	if opts.IsHypervisor {
		conf.Hypervisor = hypervisorConfigDefault()
	}

	if len(opts.HypervisorPKs) > 0 {
		conf.Hypervisors = append([]cipher.PubKey{}, opts.HypervisorPKs...)
	}

	if opts.RewardAddress != "" {
		conf.RewardAddress = opts.RewardAddress
	}

	conf.IsPublic = opts.IsPublic

	return conf, nil
}

// hypervisorConfigDefault returns a HypervisorConfig with the
// fields a fresh hypervisor needs to come up. The deeper LAN-dmsg
// keypair / cookie keys are left empty; visorconfig.V1's load path
// generates them on first persist. The form-level "enable" toggle
// is what the operator usually means by --ishv, and that's what's
// set here.
func hypervisorConfigDefault() *visorconfig.HypervisorConfig {
	hv := visorconfig.GenerateWorkDirConfig(false)
	hv.Enable = true
	return &hv
}

// MustMarshalJSON renders the given V1 as indented JSON ready for
// download from the install page. Panics on error — visorconfig.V1
// is a known-good shape and MarshalJSON never fails under normal
// conditions. The convenience wrapper is here so the WASM caller
// doesn't have to handle an impossible error in its js.FuncOf
// adapter.
//
// Implementation lives in genvisor_native.go (//go:build !js) and
// genvisor_js.go (//go:build js). The two variants exist because
// encoding/json (and its reflect runtime helpers) is the single
// largest blocker for TinyGo-compiling the install-page WASM.
// Under js the implementation walks V1's exported fields via a
// hand-rolled streaming serializer that doesn't touch reflect.
