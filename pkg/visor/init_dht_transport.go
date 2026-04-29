package visor

import (
	"context"

	"github.com/skycoin/skywire/pkg/dht"
	"github.com/skycoin/skywire/pkg/logging"
)

// initDHTTransport wires the transport-manager-dependent parts of the DHT
// (transport-layer sync over route ID 0, and the hybrid TPD client that
// reads from DHT first). Split out from initDHT so that the DHT's dmsg
// listener on port 100 comes up as soon as v.dmsgC is ready, rather than
// waiting on the full transport init chain (~20s on production), during
// which peers dial port 100 and hit "request has no associated listener".
func initDHTTransport(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.dhtNode == nil || v.tpM == nil {
		return nil
	}

	// Register transport-layer DHT handler so DHT messages can flow
	// over skywire transports (route ID 0) without DMSG.
	tlDHT := dht.NewTransportLayerDHT(v.tpM, log)
	v.tpM.SetDHTHandler(tlDHT.HandleDHTPacket)
	// Add transport peers to the DHT routing table as they become
	// reachable — this happens naturally via the DHT's own Ping RPC
	// when the transport-layer transport is used for lookups.
	v.dhtNode.AddTransport(tlDHT)
	log.Info("DHT: transport-layer sync enabled (route ID 0)")

	// Wrap the transport discovery client with a DHT hybrid client so
	// reads try DHT first, fall back to HTTP. Writes go to both.
	tpdAdapter := dht.NewTPDAdapter(v.dhtNode, log)
	v.initLock.Lock()
	v.tpM.Conf.DiscoveryClient = dht.NewHybridTPDClient(tpdAdapter, v.tpM.Conf.DiscoveryClient, log)
	v.initLock.Unlock()
	log.Info("Transport discovery: DHT-first reads enabled (HTTP fallback)")

	return nil
}
