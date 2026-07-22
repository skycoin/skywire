// Package vpn pkg/vpn/meshapp.go c4-app-vpn
//
// Shared mesh-gateway wiring for the vpn-router and vpn-client apps: build the
// MeshDial from an app.Client, parse operator-supplied aliases, and load-or-mint
// the TLS CA. Kept here so both apps configure the gateway identically.
package vpn

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skynetca"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

// MeshDialer adapts an app.Client to meshgw.MeshDial: scheme ("dmsg" / "skynet")
// is the appnet network type; port is the mesh routing port (the client's
// original destination port).
func MeshDialer(appCl *app.Client) meshgw.MeshDial {
	return func(_ context.Context, scheme string, dest cipher.PubKey, port uint16) (net.Conn, error) {
		return appCl.Dial(appnet.Addr{
			Net:    appnet.Type(scheme),
			PubKey: dest,
			Port:   routing.Port(port),
		})
	}
}

// ParseMeshAliases turns repeated `name=<pk>` strings into a friendly-name → PK
// map for the mesh gateway.
func ParseMeshAliases(in []string) (map[string]cipher.PubKey, error) {
	m := make(map[string]cipher.PubKey, len(in))
	for _, kv := range in {
		name, pkStr, ok := strings.Cut(kv, "=")
		if !ok || name == "" || pkStr == "" {
			return nil, fmt.Errorf("invalid mesh alias %q (want name=<pk>)", kv)
		}
		var pk cipher.PubKey
		if err := pk.Set(pkStr); err != nil {
			return nil, fmt.Errorf("invalid pk in mesh alias %q: %w", kv, err)
		}
		m[strings.ToLower(name)] = pk
	}
	return m, nil
}

// LoadOrCreateMeshCA loads the mesh-gateway CA from certPath/keyPath, generating
// and persisting a fresh one (permitting .dmsg/.skynet) if absent. Empty paths
// default under defaultDir. It prints the cert path + fingerprint so the operator
// can install it as trusted on the clients that will use the gateway.
func LoadOrCreateMeshCA(certPath, keyPath, defaultDir string) (skynetca.LeafMinter, error) {
	if certPath == "" {
		certPath = filepath.Join(defaultDir, "ca.pem")
	}
	if keyPath == "" {
		keyPath = filepath.Join(defaultDir, "ca.key")
	}
	ca, key, err := skynetca.LoadCA(certPath, keyPath)
	if err != nil {
		ca, key, err = skynetca.GenerateCA(skynetca.CAOptions{CommonName: "Skywire Mesh Gateway CA"})
		if err != nil {
			return nil, fmt.Errorf("generate mesh gateway CA: %w", err)
		}
		if mkErr := os.MkdirAll(filepath.Dir(certPath), 0o750); mkErr != nil {
			return nil, fmt.Errorf("mesh gateway CA dir: %w", mkErr)
		}
		if saveErr := skynetca.SaveCA(ca, key, certPath, keyPath); saveErr != nil {
			return nil, fmt.Errorf("save mesh gateway CA: %w", saveErr)
		}
	}
	fmt.Fprintf(os.Stderr, "mesh gateway TLS CA: %s (fingerprint %s) — install as trusted on clients for HTTPS to *.dmsg/*.skynet\n",
		certPath, skynetca.Fingerprint(ca))
	return skynetca.NewMinter(ca, key, skynetca.LeafOptions{}), nil
}
