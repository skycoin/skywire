//go:build linux
// +build linux

// Package commands cmd/apps/vpn-router/commands/meshgw_linux.go c4-app-vpn
package commands

import (
	"context"
	"fmt"
	"net"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/vpn"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

// runMeshGatewayOnly builds a standalone mesh-gateway config from the parsed
// flags and runs it until ctx is canceled. Linux-only: the transparent proxy +
// REDIRECT need netfilter.
func runMeshGatewayOnly(ctx context.Context, dial meshgw.MeshDial, logger logrus.FieldLogger) error {
	if lanIfc == "" {
		return fmt.Errorf("--mesh-gateway-only needs --lan-ifc (the interface LAN traffic arrives on)")
	}
	cfg := vpn.MeshGatewayConfig{
		LANInterface: lanIfc,
		DNSPort:      meshDNSPort,
		CIDR:         meshGWCIDR,
		MeshDial:     dial,
	}
	if meshBind != "" {
		if cfg.BindIP = net.ParseIP(meshBind); cfg.BindIP == nil {
			return fmt.Errorf("invalid --mesh-bind %q", meshBind)
		}
	}
	if len(meshAliases) > 0 {
		aliases, err := vpn.ParseMeshAliases(meshAliases)
		if err != nil {
			return err
		}
		cfg.Aliases = aliases
	}
	if meshTLS {
		minter, err := vpn.LoadOrCreateMeshCA(meshCACert, meshCAKey, defaultMeshCADir)
		if err != nil {
			return err
		}
		cfg.TLSMinter = minter
	}
	return vpn.RunMeshGatewayOnly(ctx, cfg, logger)
}
