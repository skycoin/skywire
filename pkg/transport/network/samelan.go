// Package network pkg/transport/network/samelan.go c2-net-transport
package network

import (
	"context"

	"github.com/skycoin/skywire/pkg/netutil"
)

type sameLANKey struct{}

// WithSameLAN marks a dial whose caller found the peer on this host's LAN.
// The dial then tries the peer's addresses on our subnets first, even when a
// VPN on one side gives the two a different public IP.
func WithSameLAN(ctx context.Context) context.Context {
	return context.WithValue(ctx, sameLANKey{}, true)
}

func onSameLAN(ctx context.Context) bool {
	v, _ := ctx.Value(sameLANKey{}).(bool) //nolint:errcheck // absent means false
	return v
}

// localNetworks is netutil.LocalNetworks, replaced in tests.
var localNetworks = netutil.LocalNetworks
