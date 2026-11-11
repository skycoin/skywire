// Package vpn pkg/vpn/tun_settings.go c4-app-vpn
package vpn

import "sync"

// TunSettings is what vpn-client asks of a tunnel it cannot configure itself:
// on iOS the packet-tunnel extension owns the interface and applies these as
// its NEPacketTunnelNetworkSettings, as Android's VpnService does with the
// establish request (tun_device_android.go). Seq grows with every change, so
// a reader can tell a new request from one it already applied.
type TunSettings struct {
	// Up is false once the client routes traffic directly again.
	Up bool `json:"up"`
	// Addr is the interface address the server assigned, in CIDR form, and
	// Gateway the server's end.
	Addr    string `json:"addr,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	MTU     int    `json:"mtu,omitempty"`
	// DNS is the resolver the tunnel should carry; empty for the system's.
	DNS string `json:"dns,omitempty"`
	Seq uint64 `json:"seq"`
}

var tunSettings struct {
	mu  sync.Mutex
	cur TunSettings
}

// CurrentTunSettings returns the last tunnel the client asked for.
func CurrentTunSettings() TunSettings {
	tunSettings.mu.Lock()
	defer tunSettings.mu.Unlock()
	return tunSettings.cur
}

// requestTun records an interface up with these parameters.
func requestTun(addrCIDR, gateway string, mtu int, dns string) TunSettings {
	tunSettings.mu.Lock()
	defer tunSettings.mu.Unlock()
	tunSettings.cur = TunSettings{Up: true, Addr: addrCIDR, Gateway: gateway, MTU: mtu, DNS: dns, Seq: tunSettings.cur.Seq + 1}
	return tunSettings.cur
}

// requestTunDown records that the client routes traffic directly again, and
// says whether that changed anything: the client drops both default halves.
func requestTunDown() (TunSettings, bool) {
	tunSettings.mu.Lock()
	defer tunSettings.mu.Unlock()
	if !tunSettings.cur.Up {
		return tunSettings.cur, false
	}
	tunSettings.cur = TunSettings{Seq: tunSettings.cur.Seq + 1}
	return tunSettings.cur, true
}
