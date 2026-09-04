//go:build js

// Package vpn pkg/vpn/tun_device_js.go c4-app-vpn
//
// js/wasm TUN construction: the browser's "TUN device" is the gVisor
// userspace netstack (netstack_tun.go) — no device node, no privileges.
package vpn

func newTUNDevice() (TUNDevice, error) {
	return newNetstackTUN()
}
