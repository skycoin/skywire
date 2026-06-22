//go:build tinygo

// Package network pkg/transport/network/sudph_tinygo.go
//
// SUDPH carrier stub for TinyGo. sudph rides kcp-go + pfilter (which pull
// quic-go) over raw UDP — none of which fits the browser/TinyGo target. The real
// constructor lives in sudph.go (//go:build !tinygo); this stub keeps those deps
// out of the TinyGo transport graph. STUN (stun_client.go) is tagged out
// alongside it (it's only used by the sudph NAT-detection path).
package network

import "errors"

func makeSudphClient(_ *resolvedClient, _ int) (Client, error) {
	return nil, errors.New("network: SUDPH transport not supported in this build (TinyGo)")
}
