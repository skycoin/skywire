// Package commands cmd/cxo/commands/cli_test.go
//
// These tests cover the CLI's pure parse/validate/dispatch layer — the glue the
// command package adds on top of pkg/cxo: hex/pubkey parsing, the interactive
// command router, per-command argument validation, and the addr/pk target
// parser. They deliberately do NOT cover the RPC-backed happy paths (those need
// a live cxo daemon and are integration-level), the interactive REPL, the
// daemon, or cobra wiring. Every case here returns before any *node.RPCClient
// method is touched, so a nil client is safe.
package commands

import (
	"strings"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validPKHex(t *testing.T) string {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk.Hex()
}

// TestPubKeyFromHex covers the three outcomes of the pubkey parser.
func TestPubKeyFromHex(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		pk, _ := cipher.GenerateKeyPair()
		got, err := pubKeyFromHex(pk.Hex())
		require.NoError(t, err)
		assert.Equal(t, pk, got)
	})

	t.Run("not hex", func(t *testing.T) {
		_, err := pubKeyFromHex("zzzz")
		assert.Error(t, err)
	})

	t.Run("wrong length", func(t *testing.T) {
		_, err := pubKeyFromHex("deadbeef") // 4 bytes, not 33
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid PubKey length")
	})
}

// TestMatchPrefix verifies the case-insensitive prefix match used by the router.
func TestMatchPrefix(t *testing.T) {
	assert.True(t, matchPrefix("Share Feed 039d", "share feed"))
	assert.True(t, matchPrefix("LIST FEEDS", "list feeds"))
	assert.False(t, matchPrefix("listfeeds", "list feeds"))
	assert.False(t, matchPrefix("tcp", "tcp connect"))
}

// TestExecuteInteractiveCommandRouting covers the dispatcher branches that
// return before touching the RPC client: empty input and unknown commands.
func TestExecuteInteractiveCommandRouting(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		assert.NoError(t, executeInteractiveCommand(nil, ""))
	})

	t.Run("whitespace only", func(t *testing.T) {
		assert.NoError(t, executeInteractiveCommand(nil, "   \t  "))
	})

	t.Run("unknown command", func(t *testing.T) {
		err := executeInteractiveCommand(nil, "frobnicate the feed")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown command")
	})
}

// TestExecArgValidation covers the usage-error guards of the interactive command
// implementations — each fires before any RPC call.
func TestExecArgValidation(t *testing.T) {
	cases := []struct {
		name  string
		run   func() error
		match string
	}{
		{"share feed missing pk", func() error { return execShareFeed(nil, []string{"share", "feed"}) }, "usage: share feed"},
		{"dont share feed missing pk", func() error { return execDontShareFeed(nil, []string{"don't", "share", "feed"}) }, "usage: don't share feed"},
		{"is sharing missing pk", func() error { return execIsSharing(nil, []string{"is", "shareing"}) }, "usage: is shareing"},
		{"tcp connect missing addr", func() error { return execTCPConnect(nil, []string{"tcp", "connect"}) }, "usage: tcp connect"},
		{"tcp disconnect missing addr", func() error { return execTCPDisconnect(nil, []string{"tcp", "disconnect"}) }, "usage: tcp disconnect"},
		{"tcp subscribe missing pk", func() error { return execTCPSubscribe(nil, []string{"tcp", "subscribe", "1.2.3.4:80"}) }, "usage: tcp subscribe"},
		{"tcp unsubscribe missing pk", func() error { return execTCPUnsubscribe(nil, []string{"tcp", "unsubscribe", "1.2.3.4:80"}) }, "usage: tcp unsubscribe"},
		{"udp connect missing addr", func() error { return execUDPConnect(nil, []string{"udp", "connect"}) }, "usage: udp connect"},
		{"udp disconnect missing addr", func() error { return execUDPDisconnect(nil, []string{"udp", "disconnect"}) }, "usage: udp disconnect"},
		{"udp subscribe missing pk", func() error { return execUDPSubscribe(nil, []string{"udp", "subscribe", "1.2.3.4:80"}) }, "usage: udp subscribe"},
		{"udp unsubscribe missing pk", func() error { return execUDPUnsubscribe(nil, []string{"udp", "unsubscribe", "1.2.3.4:80"}) }, "usage: udp unsubscribe"},
		{"connections of feed missing pk", func() error { return execConnectionsOfFeed(nil, []string{"connections", "of", "feed"}) }, "usage: connections of feed"},
		{"root info too few", func() error { return execRootInfo(nil, []string{"root", "info", "pk"}) }, "usage: root info"},
		{"root tree too few", func() error { return execRootTree(nil, []string{"root", "tree", "pk"}) }, "usage: root tree"},
		{"last root missing pk", func() error { return execLastRoot(nil, []string{"last", "root"}) }, "usage: last root"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.match)
		})
	}
}

// TestExecBadPubKey covers the pubkey-parse failure branch of the commands that
// parse a key, which also returns before any RPC call.
func TestExecBadPubKey(t *testing.T) {
	bad := "nothex"
	cases := []struct {
		name string
		run  func() error
	}{
		{"share feed", func() error { return execShareFeed(nil, []string{"share", "feed", bad}) }},
		{"dont share feed", func() error { return execDontShareFeed(nil, []string{"don't", "share", "feed", bad}) }},
		{"is sharing", func() error { return execIsSharing(nil, []string{"is", "shareing", bad}) }},
		{"tcp subscribe", func() error { return execTCPSubscribe(nil, []string{"tcp", "subscribe", "1.2.3.4:80", bad}) }},
		{"udp unsubscribe", func() error { return execUDPUnsubscribe(nil, []string{"udp", "unsubscribe", "1.2.3.4:80", bad}) }},
		{"connections of feed", func() error { return execConnectionsOfFeed(nil, []string{"connections", "of", "feed", bad}) }},
		{"last root", func() error { return execLastRoot(nil, []string{"last", "root", bad}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Error(t, tc.run())
		})
	}
}

// TestExecRootInfoNumericValidation covers the nonce/seq ParseUint guards, which
// run after a valid pubkey but before any RPC call.
func TestExecRootInfoNumericValidation(t *testing.T) {
	pk := validPKHex(t)

	t.Run("bad nonce", func(t *testing.T) {
		assert.Error(t, execRootInfo(nil, []string{"root", "info", pk, "notanumber", "0"}))
	})
	t.Run("bad seq", func(t *testing.T) {
		assert.Error(t, execRootInfo(nil, []string{"root", "info", pk, "1", "notanumber"}))
	})
	t.Run("root tree bad nonce", func(t *testing.T) {
		assert.Error(t, execRootTree(nil, []string{"root", "tree", pk, "x", "0"}))
	})
}

// TestAddrAndPK covers every branch of the dual-form target parser.
func TestAddrAndPK(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	pkHex := pk.Hex()

	t.Run("pk@addr form", func(t *testing.T) {
		addr, gotPK, err := addrAndPK([]string{pkHex + "@1.2.3.4:8870"})
		require.NoError(t, err)
		assert.Equal(t, "1.2.3.4:8870", addr)
		assert.Equal(t, pk, gotPK)
	})

	t.Run("single arg without @", func(t *testing.T) {
		_, _, err := addrAndPK([]string{pkHex})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "single argument must be")
	})

	t.Run("missing address after @", func(t *testing.T) {
		_, _, err := addrAndPK([]string{pkHex + "@"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing address after")
	})

	t.Run("bad pk in @ form", func(t *testing.T) {
		_, _, err := addrAndPK([]string{"nothex@1.2.3.4:80"})
		assert.Error(t, err)
	})

	t.Run("two-arg form", func(t *testing.T) {
		addr, gotPK, err := addrAndPK([]string{"1.2.3.4:8870", pkHex})
		require.NoError(t, err)
		assert.Equal(t, "1.2.3.4:8870", addr)
		assert.Equal(t, pk, gotPK)
	})

	t.Run("two-arg bad pk", func(t *testing.T) {
		_, _, err := addrAndPK([]string{"1.2.3.4:8870", "nothex"})
		assert.Error(t, err)
	})

	t.Run("wrong arg count", func(t *testing.T) {
		_, _, err := addrAndPK([]string{"a", "b", "c"})
		require.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "expected")
	})
}
