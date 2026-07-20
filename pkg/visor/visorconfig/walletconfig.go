// Package visorconfig pkg/visor/visorconfig/walletconfig.go c3-app-wallet
package visorconfig

// Wallet custody modes — WHERE the keys and wallet files live. This is the one
// axis the operator actually chooses; how the frontend is served and how the
// node API is reached vary by platform and are not the operator's concern.
//
// See docs/design/skycoin-web-wallet-architecture.md.
const (
	// WalletCustodyBrowser keeps keys in the browser (localStorage on a native
	// visor or `hv serve`, an OPFS virtual directory under wasm). The host
	// never sees them. Available EVERYWHERE, because the wallet frontend is
	// always a browser SPA — hence the default.
	WalletCustodyBrowser = "browser"

	// WalletCustodyDisk keeps wallet files on a real filesystem, served by the
	// skycoin-web server (RunSkycoinWeb) at Wallet.Dir. Requires a backend Go
	// process AND a filesystem, so it is offered only on a native visor or
	// `hv serve` — never in a statically-hosted wasm PWA or a single-file
	// hypervisor. Flips the trust model: the host holds the keys.
	WalletCustodyDisk = "disk"

	// WalletCustodyRemote delegates to another visor's skycoin-web server over
	// dmsg (Wallet.RemotePK). Available anywhere a dmsg target is reachable;
	// you are trusting that remote.
	WalletCustodyRemote = "remote"
)

// WalletConfig is the single config block behind the wallet feature, in all
// three realizations (native visor, `hv serve`, wasm/single-file). The block is
// uniform; what DEGRADES per context is which custody modes can actually be
// realized — a context with no backend filesystem offers only browser|remote.
// Keeping one block rather than per-context knobs is what stops the surfaces
// drifting apart.
//
// The existing skycoinweb* config-gen flags (--skycoinweb, --skycoinwebwallet,
// --skycoinwebaddr, --skycoinwebnodes, --skycoinwebuser) become aliases into
// this block rather than a parallel source of truth.
type WalletConfig struct {
	// Serve controls whether the /wallet/ frontend is served at all — the
	// feature on/off switch. Deliberately a CLI/config knob rather than a GUI
	// self-toggle: a GUI that can turn off the surface hosting it is a trap.
	Serve bool `json:"serve"`

	// Custody is browser | disk | remote (the WalletCustody* constants).
	// Empty means browser. A value the current context cannot realize is
	// clamped by WalletCustody() rather than failing the config load, so a
	// config written on a native visor still loads under wasm.
	Custody string `json:"custody,omitempty"`

	// Dir is the disk-custody wallet directory. It is a SEED-wallet store —
	// one directory holding N seed files — NOT one directory per coin. The
	// per-coin-directory layout is legacy: since bip44 carries coin_type per
	// account, one seed file already spans several chains.
	Dir string `json:"dir,omitempty"`

	// User is the account to drop to when running the wallet server, for
	// visors running as a system user (_skywire) that should not own the
	// operator's wallet files. Mirrors SKYCOINWEBUSER. Disk custody only.
	User string `json:"user,omitempty"`

	// RemotePK is the visor whose skycoin-web server holds the wallets, for
	// remote custody.
	RemotePK string `json:"remote_pk,omitempty"`
}

// WalletServe reports whether the wallet frontend should be served. Nil-safe:
// a config predating this block (or with the block omitted) serves the wallet,
// preserving existing behavior — the block is opt-OUT, not opt-in.
func (v *V1) WalletServe() bool {
	if v == nil {
		return false
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.Wallet == nil {
		return true
	}
	return v.Wallet.Serve
}

// WalletCustody returns the effective custody mode, defaulting to browser and
// clamping to what this build can actually realize.
//
// The clamp is the point of the capability gating: disk needs a backend process
// with a filesystem. Under wasm there is none, so a config carrying
// custody:"disk" — perfectly valid on the native visor that wrote it — silently
// degrades to browser instead of leaving the UI offering a mode that cannot
// work. See walletCustodyDiskCapable (per-build).
func (v *V1) WalletCustody() string {
	if v == nil {
		return WalletCustodyBrowser
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.Wallet == nil || v.Wallet.Custody == "" {
		return WalletCustodyBrowser
	}
	switch v.Wallet.Custody {
	case WalletCustodyDisk:
		if !walletCustodyDiskCapable {
			return WalletCustodyBrowser
		}
		return WalletCustodyDisk
	case WalletCustodyRemote:
		return WalletCustodyRemote
	default:
		return WalletCustodyBrowser
	}
}

// WalletDir returns the disk-custody wallet directory (empty unless disk
// custody is both selected and realizable).
func (v *V1) WalletDir() string {
	if v.WalletCustody() != WalletCustodyDisk {
		return ""
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.Wallet == nil {
		return ""
	}
	return v.Wallet.Dir
}

// WalletRemotePK returns the remote-custody target (empty unless remote).
func (v *V1) WalletRemotePK() string {
	if v.WalletCustody() != WalletCustodyRemote {
		return ""
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.Wallet == nil {
		return ""
	}
	return v.Wallet.RemotePK
}

// WalletCustodyOptions lists the custody modes this build can realize — what a
// GUI should offer. browser and remote are always available; disk appears only
// where there is a backend process with a filesystem.
func WalletCustodyOptions() []string {
	if walletCustodyDiskCapable {
		return []string{WalletCustodyBrowser, WalletCustodyDisk, WalletCustodyRemote}
	}
	return []string{WalletCustodyBrowser, WalletCustodyRemote}
}
