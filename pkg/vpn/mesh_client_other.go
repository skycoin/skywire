//go:build !linux

// Package vpn pkg/vpn/mesh_client_other.go c3-app-vpn
package vpn

import (
	"context"
	"errors"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skydns"
)

// meshClientGateway is a no-op stub; the client mesh gateway (loopback resolver
// + OUTPUT-chain REDIRECT) is Linux-only, like the vpn-router.
type meshClientGateway struct{}

func (m *meshClientGateway) stop() {}

func (m *meshClientGateway) engine() *skydns.Engine { return nil }

func (c *Client) startMeshGateway() (*meshClientGateway, error) {
	return startMeshClientGateway(context.Background(), c.cfg, logging.StandardLogger())
}

func startMeshClientGateway(_ context.Context, _ ClientConfig, _ logging.FieldLogger) (*meshClientGateway, error) {
	return nil, errors.New("vpn: mesh gateway is only supported on Linux and Android")
}
