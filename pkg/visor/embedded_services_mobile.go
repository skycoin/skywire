//go:build mobile

// Package visor pkg/visor/embedded_services_mobile.go c3-vis-core
//
// The mobile build has no embedded deployment services. The module is
// dropped (init_modules_mobile.go), and the two files that run the services
// (init_embedded_services.go, embedded_services_cxo.go) are tagged out: the
// service factories import pkg/metricsutil, which brings 0magnet/metrics
// back into the iOS import graph. What is left here is what the shared code
// still calls. The CXO publishers keep their usual storage, none is lent to
// a service, and `visor state` lists no services.
package visor

import (
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// embeddedSet is empty: config.embedded_services is not read on a phone.
type embeddedSet struct{}

// hostCXOPubStorage is cxoPubStorage: no embedded service aggregates on any
// port.
func (*Visor) hostCXOPubStorage(dataDir string, _ uint16) (string, bool) {
	return cxoPubStorage(dataDir)
}

// trackCXOPublisher records nothing: no embedded service borrows a publisher.
func (*Visor) trackCXOPublisher(uint16, *treestore.Publisher) {}

// embeddedServiceStates reports no services for `visor state`.
func (*Visor) embeddedServiceStates() []visorapi.EmbeddedServiceState {
	return nil
}
