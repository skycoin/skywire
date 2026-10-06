//go:build !android

// Package vpn pkg/vpn/skydns_other.go c4-app-vpn
package vpn

import (
	"context"
	"errors"
)

// RunSkyDNS needs the phone's VPN service, so it only runs on Android.
func RunSkyDNS(_ context.Context, _ SkyDNSConfig) error {
	return errors.New("vpn: SkyDNS on its own tunnel only runs on Android")
}
