//go:build !android && !linux

// Package vpn pkg/vpn/skydns_other.go c4-app-vpn
package vpn

import (
	"context"
	"errors"
)

// RunSkyDNS needs a tunnel of its own, which it can only take on Linux and Android.
func RunSkyDNS(_ context.Context, _ SkyDNSConfig) error {
	return errors.New("vpn: SkyDNS on its own tunnel only runs on Linux and Android")
}
