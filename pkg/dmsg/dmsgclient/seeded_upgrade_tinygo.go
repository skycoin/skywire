//go:build tinygo

// Package dmsgclient pkg/dmsg/dmsgclient/seeded_upgrade_tinygo.go
package dmsgclient

import (
	"context"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// upgradeDiscovery (TinyGo) backs the registering-fallback discovery with the
// net/http-free dmsgDiscClient, which speaks HTTP/1.1 straight over a dmsg
// stream. This is what lets the wasm hypervisor register itself and resolve
// peers under TinyGo, where net/http (hence dmsghttp) won't compile on the js
// target. ctx is unused here (NewDmsgDiscClient dials lazily per call) but kept
// for signature parity with the native build.
func upgradeDiscovery(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client, dClient disc.APIClient, discDmsgAddr string) error {
	dmsgDisc, err := NewDmsgDiscClient(dmsgC, discDmsgAddr, log)
	if err != nil {
		return err
	}
	dmsgC.SetDiscoveryClients([]disc.APIClient{NewRegisteringFallbackDiscClient(dClient, dmsgDisc, log)})
	return nil
}
