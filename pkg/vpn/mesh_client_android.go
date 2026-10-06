//go:build android

// Package vpn pkg/vpn/mesh_client_android.go c4-app-vpn
package vpn

import (
	"errors"
	"io"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/skydns"
)

// meshClientGateway is SkyDNS inside the tunnel. Every packet of the phone's
// TUN already passes through this process, so nothing has to be redirected.
type meshClientGateway struct {
	sky *skydns.Engine
}

func (c *Client) startMeshGateway() (*meshClientGateway, error) {
	server := c.cfg.DNSAddr
	if server == "" {
		server = shareDefaultDNS
	}
	// Other names are asked through the tunnel, so the exit's resolver path is
	// the one used. Strict TLS wherever the resolver is known to offer it.
	up := skydns.NewUpstream(server, c.shareDial, skydns.HasTLS(server))
	sky, err := skydns.New(skydns.Config{
		Dial:     c.cfg.MeshDial,
		Upstream: up,
		Aliases:  c.cfg.MeshAliases,
		Minter:   c.cfg.MeshTLSMinter,
		MTU:      TUNMTU,
		Log:      logrus.StandardLogger(),
	})
	if err != nil {
		return nil, err
	}
	go c.pumpSkyDNS(sky)
	return &meshClientGateway{sky: sky}, nil
}

func (m *meshClientGateway) stop() { _ = m.sky.Close() } //nolint:errcheck

func (m *meshClientGateway) engine() *skydns.Engine {
	if m == nil {
		return nil
	}
	return m.sky
}

// pumpSkyDNS writes SkyDNS's packets into the TUN until the engine closes.
func (c *Client) pumpSkyDNS(sky *skydns.Engine) {
	buf := make([]byte, TUNMTU+4)
	for {
		n, err := sky.Read(buf)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			continue
		}
		c.tunMu.Lock()
		tun := c.tun
		c.tunMu.Unlock()
		if tun != nil {
			_, _ = tun.Write(buf[:n]) //nolint:errcheck
		}
	}
}
