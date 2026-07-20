// Package visorconfig pkg/visor/visorconfig/walletconfig_test.go c3-app-wallet
package visorconfig

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWalletDefaultsPreserveExistingBehavior pins the opt-OUT contract: a
// config written before the wallet block existed (Wallet == nil) must still
// serve the wallet, with browser custody. If this flips, every deployed config
// silently loses its wallet on upgrade.
func TestWalletDefaultsPreserveExistingBehavior(t *testing.T) {
	v := &V1{}
	require.True(t, v.WalletServe(), "nil Wallet block must still serve the wallet")
	require.Equal(t, WalletCustodyBrowser, v.WalletCustody())
	require.Empty(t, v.WalletDir())
	require.Empty(t, v.WalletRemotePK())
}

func TestWalletServeOptOut(t *testing.T) {
	v := &V1{Wallet: &WalletConfig{Serve: false}}
	require.False(t, v.WalletServe())

	v = &V1{Wallet: &WalletConfig{Serve: true}}
	require.True(t, v.WalletServe())
}

// TestWalletCustodyClamp covers the capability gate. On a build with a
// filesystem, disk is honored; on a browser build it must clamp to browser
// rather than offering a mode that cannot work. Asserting against the same
// constant the production code uses keeps this meaningful under both builds
// instead of only the one CI happens to run.
func TestWalletCustodyClamp(t *testing.T) {
	v := &V1{Wallet: &WalletConfig{Serve: true, Custody: WalletCustodyDisk, Dir: "/tmp/w"}}

	if walletCustodyDiskCapable {
		require.Equal(t, WalletCustodyDisk, v.WalletCustody())
		require.Equal(t, "/tmp/w", v.WalletDir())
	} else {
		require.Equal(t, WalletCustodyBrowser, v.WalletCustody(),
			"disk custody must clamp to browser where there is no filesystem")
		require.Empty(t, v.WalletDir(), "no wallet dir once clamped away from disk")
	}
}

func TestWalletCustodyRemote(t *testing.T) {
	pk := "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"
	v := &V1{Wallet: &WalletConfig{Serve: true, Custody: WalletCustodyRemote, RemotePK: pk}}
	require.Equal(t, WalletCustodyRemote, v.WalletCustody())
	require.Equal(t, pk, v.WalletRemotePK())
	require.Empty(t, v.WalletDir(), "remote custody has no local dir")
}

// An unrecognized custody value must not brick the wallet — fall back to the
// safe default rather than erroring the config load.
func TestWalletCustodyUnknownFallsBackToBrowser(t *testing.T) {
	v := &V1{Wallet: &WalletConfig{Serve: true, Custody: "nonsense"}}
	require.Equal(t, WalletCustodyBrowser, v.WalletCustody())
}

func TestWalletCustodyOptionsMatchCapability(t *testing.T) {
	opts := WalletCustodyOptions()
	require.Contains(t, opts, WalletCustodyBrowser)
	require.Contains(t, opts, WalletCustodyRemote)
	if walletCustodyDiskCapable {
		require.Contains(t, opts, WalletCustodyDisk)
	} else {
		require.NotContains(t, opts, WalletCustodyDisk,
			"must not offer disk custody where it cannot be realized")
	}
}

// The block must survive the v1JSON compat mirror — that mirror is hand-kept in
// sync with V1, so a field added to one and not the other is silently dropped
// on every config read/write cycle.
func TestWalletSurvivesCompatRoundTrip(t *testing.T) {
	in := &V1{Wallet: &WalletConfig{
		Serve:   true,
		Custody: WalletCustodyDisk,
		Dir:     "/home/operator/.skycoin/wallets",
		User:    "operator",
	}}

	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.Contains(t, string(b), `"wallet"`)

	var out V1
	require.NoError(t, json.Unmarshal(b, &out))
	require.NotNil(t, out.Wallet, "wallet block dropped by the compat mirror")
	require.Equal(t, WalletCustodyDisk, out.Wallet.Custody)
	require.Equal(t, "/home/operator/.skycoin/wallets", out.Wallet.Dir)
	require.Equal(t, "operator", out.Wallet.User)
	require.True(t, out.Wallet.Serve)
}

// A config with no wallet block must not grow one on marshal — otherwise every
// existing config file gains noise on the next write.
func TestWalletOmittedWhenUnset(t *testing.T) {
	b, err := json.Marshal(&V1{})
	require.NoError(t, err)
	require.NotContains(t, string(b), `"wallet"`)
}
