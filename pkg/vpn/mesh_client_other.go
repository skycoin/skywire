//go:build !linux
// +build !linux

// Package vpn pkg/vpn/mesh_client_other.go c4-app-vpn
package vpn

import (
	"context"
	"errors"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/skydns"
)

// meshClientGateway is a no-op stub; the client mesh gateway (loopback resolver
// + OUTPUT-chain REDIRECT) is Linux-only, like the vpn-router.
type meshClientGateway struct{}

func (m *meshClientGateway) stop() {}

func (m *meshClientGateway) engine() *skydns.Engine { return nil }

func (c *Client) startMeshGateway() (*meshClientGateway, error) {
	return startMeshClientGateway(context.Background(), c.cfg, logrus.StandardLogger())
}

func startMeshClientGateway(_ context.Context, _ ClientConfig, _ logrus.FieldLogger) (*meshClientGateway, error) {
	return nil, errors.New("vpn: mesh gateway is only supported on Linux and Android")
}
