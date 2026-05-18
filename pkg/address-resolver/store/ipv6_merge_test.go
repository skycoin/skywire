//go:build !no_ci
// +build !no_ci

// Package store — pkg/address-resolver/store/ipv6_merge_test.go
//
// Verifies the per-family merge semantics added in #1525 Phase 1:
// a dual-stack visor binds twice (once over IPv4 HTTP, once over
// IPv6 HTTP). Each Bind carries exactly one of RemoteAddr /
// RemoteAddrV6 (the family the AR server captured from the
// connecting socket). After the second Bind, Resolve must return
// BOTH addrs — naive overwrite would clobber the first family.
//
// Backward-compat: a v4-only visor (RemoteAddrV6 stays empty)
// continues to behave exactly as today.
package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestMemoryStore_BindMergesFamilyAddrs(t *testing.T) {
	ctx := context.Background()
	s := newMemoryStore()
	pk, _ := cipher.GenerateKeyPair()

	// Bind 1: IPv4 only (simulates visor's v4 HTTP bind).
	require.NoError(t, s.Bind(ctx, types.STCPR, pk, addrresolver.VisorData{
		RemoteAddr: "203.0.113.5",
	}))
	got, err := s.Resolve(ctx, types.STCPR, pk)
	require.NoError(t, err)
	require.Equal(t, "203.0.113.5", got.RemoteAddr)
	require.Empty(t, got.RemoteAddrV6)

	// Bind 2: IPv6 only (simulates visor's v6 HTTP bind). Merge
	// must preserve the prior v4 addr.
	require.NoError(t, s.Bind(ctx, types.STCPR, pk, addrresolver.VisorData{
		RemoteAddrV6: "2001:db8::1",
	}))
	got, err = s.Resolve(ctx, types.STCPR, pk)
	require.NoError(t, err)
	require.Equal(t, "203.0.113.5", got.RemoteAddr, "v4 addr must survive subsequent v6-only bind")
	require.Equal(t, "2001:db8::1", got.RemoteAddrV6)

	// Bind 3: refresh v4 with a NEW address. Merge must replace
	// the v4 addr but keep the v6 from the prior bind.
	require.NoError(t, s.Bind(ctx, types.STCPR, pk, addrresolver.VisorData{
		RemoteAddr: "203.0.113.99",
	}))
	got, err = s.Resolve(ctx, types.STCPR, pk)
	require.NoError(t, err)
	require.Equal(t, "203.0.113.99", got.RemoteAddr, "fresh v4 bind replaces prior v4")
	require.Equal(t, "2001:db8::1", got.RemoteAddrV6, "v6 addr must survive v4-only re-bind")
}

func TestMemoryStore_BindV4OnlyBackwardCompat(t *testing.T) {
	// Pre-#1525 callers populate only RemoteAddr and expect Resolve
	// to round-trip unchanged. The merge logic must not introduce
	// any v6 ghost into a never-v6 record.
	ctx := context.Background()
	s := newMemoryStore()
	pk, _ := cipher.GenerateKeyPair()

	require.NoError(t, s.Bind(ctx, types.STCPR, pk, addrresolver.VisorData{
		RemoteAddr: "203.0.113.5",
	}))
	require.NoError(t, s.Bind(ctx, types.STCPR, pk, addrresolver.VisorData{
		RemoteAddr: "203.0.113.5", // 90s refresh, same addr
	}))
	got, err := s.Resolve(ctx, types.STCPR, pk)
	require.NoError(t, err)
	require.Equal(t, "203.0.113.5", got.RemoteAddr)
	require.Empty(t, got.RemoteAddrV6)
}
