//go:build tinygo

// Package dmsgclient pkg/dmsg/dmsgclient/seeded_upgrade_tinygo.go
package dmsgclient

import (
	"context"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// upgradeDiscovery (TinyGo) installs the net/http-free seededDiscClient: it talks
// to the REAL discovery (the dmsgDiscClient, HTTP/1.1 over a dmsg stream) for
// registering + resolving + the live server list, with a small shortcut for the
// discovery PK + seed servers (recursion-avoidance + bootstrap). This is what
// lets the wasm client actually REGISTER in discovery and learn servers from it,
// instead of resolving everything from the seeded direct client. ctx is unused
// (NewDmsgDiscClient dials lazily per call); dClient (the bootstrap direct
// client) is intentionally NOT consulted for discovery queries.
func upgradeDiscovery(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client, dClient disc.APIClient, seedServers []*disc.Entry, discDmsgAddr string) error {
	real, err := NewDmsgDiscClient(dmsgC, discDmsgAddr, log)
	if err != nil {
		return err
	}

	shortcut := make(map[cipher.PubKey]*disc.Entry)
	var seedPKs []cipher.PubKey
	for _, s := range seedServers {
		shortcut[s.Static] = s
		seedPKs = append(seedPKs, s.Static)
	}
	// Synthetic discovery entry (delegated to the seed servers) so a DialStream to
	// the discovery PK bridges via a seed session without recursing into discovery.
	if hex := dmsg.ExtractPKFromDmsgAddr(discDmsgAddr); hex != "" {
		var discPK cipher.PubKey
		if err := discPK.UnmarshalText([]byte(hex)); err == nil {
			shortcut[discPK] = &disc.Entry{Version: "0.0.1", Static: discPK, Client: &disc.Client{DelegatedServers: seedPKs}}
		}
	}

	dmsgC.SetDiscoveryClients([]disc.APIClient{NewSeededDiscClient(real, shortcut, seedServers, log)})
	return nil
}
