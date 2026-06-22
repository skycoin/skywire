//go:build tinygo

// Package network pkg/transport/network/client_tinygo.go
//
// TinyGo stub for the address-resolver-backed carriers. STCPR/SUDPH/QUIC ride
// the AR (net/http) + raw UDP/TCP (and quic-go/kcp), none of which compile or run
// on the TinyGo/browser target — so MakeClient's fall-through here just reports
// them unsupported. The real implementation is client_resolved.go (!tinygo).
// Only dmsg (and AR-free stcp) carriers are available under TinyGo.
package network

import (
	"fmt"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

func (f *ClientFactory) makeResolvedClient(netType types.Type, _ *genericClient, _ int) (Client, error) {
	return nil, fmt.Errorf("network: %s transport not supported in this build (TinyGo)", netType)
}
