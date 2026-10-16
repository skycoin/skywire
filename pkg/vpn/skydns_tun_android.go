//go:build android

// Package vpn pkg/vpn/skydns_tun_android.go c4-app-vpn
package vpn

import "errors"

// SetupSkyDNSTun is not used on Android, where the app's VPN service owns the
// tunnel.
func SetupSkyDNSTun(_, _ int) error { return errors.New("skydns: --setup-tun is not used on Android") }
