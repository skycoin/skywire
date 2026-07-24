//go:build linux
// +build linux

// Package vpn pkg/vpn/mesh_gateway_standalone_linux_test.go c4-app-vpn
package vpn

import (
	"context"
	"net"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestMeshGatewayConfigDefaults(t *testing.T) {
	// Zero DNSPort → 53; zero BindIP → 0.0.0.0.
	var c MeshGatewayConfig
	if got := c.dnsPort(); got != defaultMeshGatewayDNSPort {
		t.Errorf("dnsPort() = %d; want %d", got, defaultMeshGatewayDNSPort)
	}
	if got := c.bindHost(); got != "0.0.0.0" {
		t.Errorf("bindHost() = %q; want 0.0.0.0", got)
	}

	// Explicit values are preserved.
	c = MeshGatewayConfig{DNSPort: 5353, BindIP: net.ParseIP("192.168.1.50")}
	if got := c.dnsPort(); got != 5353 {
		t.Errorf("dnsPort() = %d; want 5353", got)
	}
	if got := c.bindHost(); got != "192.168.1.50" {
		t.Errorf("bindHost() = %q; want 192.168.1.50", got)
	}
}

// The two pre-flight checks fire before any privileged (netfilter / sysctl)
// operation, so they are safe to assert without root.
func TestRunMeshGatewayOnlyPreflight(t *testing.T) {
	okDial := func(_ context.Context, _ string, _ cipher.PubKey, _ uint16) (net.Conn, error) {
		return nil, nil //nolint:nilnil // never called in these error paths
	}

	if err := RunMeshGatewayOnly(context.Background(), MeshGatewayConfig{MeshDial: okDial}, nil); err == nil {
		t.Error("expected error when LANInterface is empty")
	}
	if err := RunMeshGatewayOnly(context.Background(), MeshGatewayConfig{LANInterface: "eth0"}, nil); err == nil {
		t.Error("expected error when MeshDial is nil")
	}
}
