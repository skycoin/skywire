package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dht"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

func initDHT(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.DHT

	if v.dmsgC == nil {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-v.dmsgC.Ready():
	}

	var bootstrapPKs, whitelistedPKs, trustedPKs []cipher.PubKey
	var fullNode bool

	if conf != nil {
		bootstrapPKs = conf.BootstrapPKs
		fullNode = conf.FullNode
		whitelistedPKs = conf.WhitelistedPKs
		trustedPKs = conf.TrustedPKs
	}
	if len(bootstrapPKs) == 0 {
		bootstrapPKs = deployment.Prod.DHTBootstrapPKs()
	}

	dhtCfg := dht.Config{
		BootstrapPKs:   bootstrapPKs,
		FullNode:       fullNode,
		WhitelistedPKs: whitelistedPKs,
		TrustedPKs:     trustedPKs,
	}

	// Persistence config.
	if conf != nil {
		dhtCfg.PersistPath = conf.PersistPath
		dhtCfg.RedisAddr = conf.RedisAddr
		dhtCfg.RedisPassword = conf.RedisPassword
		dhtCfg.RedisDB = conf.RedisDB
	}
	// Default bbolt path if no explicit persistence configured.
	if dhtCfg.PersistPath == "" && dhtCfg.RedisAddr == "" && v.conf.LocalPath != "" {
		dhtCfg.PersistPath = v.conf.LocalPath + "/dht.db"
	}

	dmsgTP := dht.NewDMSGTransport(v.dmsgC)
	node := dht.New(dhtCfg, v.conf.PK, v.conf.SK, dmsgTP, log)

	// Set up persistence backend.
	backend, err := dht.NewBackendFromConfig(&dhtCfg)
	if err != nil {
		log.WithError(err).Warn("DHT persistence backend failed — running in-memory only")
	} else {
		if err := node.Store().SetBackend(backend); err != nil {
			log.WithError(err).Warn("DHT backend rehydration failed")
		} else {
			log.WithField("items_loaded", node.Store().Len()).Info("DHT store rehydrated from backend")
		}
	}

	if err := node.Start(ctx); err != nil {
		return err
	}

	v.initLock.Lock()
	v.dhtNode = node
	v.initLock.Unlock()

	v.pushCloseStack("dht", func() error {
		return node.Stop()
	})

	// Wire DHT lookup into the DMSG client so DialStream checks the
	// local DHT store before falling back to the HTTP discovery.
	// This resolves PKs that publish their DMSG entry to the DHT
	// without any network round-trip.
	if v.dmsgC != nil {
		v.dmsgC.SetDHTLookup(func(pk cipher.PubKey) (*dmsgdisc.Entry, error) {
			target := dht.MutableItemTarget(pk, []byte("dmsg"))
			item := node.Store().Get(target)
			if item == nil {
				return nil, fmt.Errorf("not in DHT")
			}
			var entry dmsgdisc.Entry
			if err := json.Unmarshal(item.V, &entry); err != nil {
				return nil, err
			}
			return &entry, nil
		})
		log.Info("DHT: wired DMSG client lookup to local DHT store")
	}

	// Register transport-layer DHT handler so DHT messages can flow
	// over skywire transports (route ID 0) without DMSG.
	if v.tpM != nil {
		tlDHT := dht.NewTransportLayerDHT(v.tpM, log)
		v.tpM.SetDHTHandler(tlDHT.HandleDHTPacket)
		// Add transport peers to the DHT routing table as they become
		// reachable — this happens naturally via the DHT's own Ping RPC
		// when the transport-layer transport is used for lookups.
		node.AddTransport(tlDHT)
		log.Info("DHT: transport-layer sync enabled (route ID 0)")
	}

	log.WithField("id", node.ID().String()).
		WithField("bootstrap_peers", len(bootstrapPKs)).
		WithField("full_node", fullNode).
		Info("DHT node started")

	// Advertise this node as a full node so other visors can discover it.
	if fullNode {
		go dht.AdvertiseFullNode(ctx, node, log)
	}

	// Wrap discovery clients with DHT hybrid clients so reads try
	// DHT first, fall back to HTTP. Writes go to both.
	discAdapter := dht.NewDiscAdapter(node, log)
	discAdapter.PopulateServerCache()
	v.initLock.Lock()
	if v.dClient != nil {
		v.dClient = dht.NewHybridDiscClient(discAdapter, v.dClient, log)
		log.Info("DMSG discovery: DHT-first reads enabled (HTTP fallback)")
	}
	if v.tpM != nil {
		tpdAdapter := dht.NewTPDAdapter(node, log)
		v.tpM.Conf.DiscoveryClient = dht.NewHybridTPDClient(tpdAdapter, v.tpM.Conf.DiscoveryClient, log)
		log.Info("Transport discovery: DHT-first reads enabled (HTTP fallback)")
	}
	v.initLock.Unlock()

	// Start background publish: mirror visor's entries to the DHT.
	// Once the DHT has peers, tell the transport manager to skip
	// HTTP re-registration — the DHT handles transport discovery.
	go func() {
		dhtPublishLoop(ctx, v, node, log)
	}()
	go func() {
		// Wait for DHT to bootstrap before switching off HTTP registration.
		// TODO: Re-enable once DiscoveryPusher (DHT → TPD) is verified
		// working reliably. Currently premature — skipping TPD registration
		// causes transports to disappear from the HTTP API even though
		// they exist on the visor and in the DHT.
		_ = node
	}()

	return nil
}

// dhtPublishLoop periodically publishes the visor's discovery entries
// to the DHT for decentralized lookup.
func dhtPublishLoop(ctx context.Context, v *Visor, node *dht.Node, log *logging.Logger) {
	// Give the visor time to fully initialize.
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}

	// Use wall-clock nanoseconds as a monotonic seq generator. This
	// survives restarts (in-memory store reset) and is essentially
	// guaranteed to overtake whatever the DHT has cached for our PK
	// from a previous incarnation. We also cross-check against the
	// network: if any peer reports a higher seq for our PK, climb
	// above it (handles backwards clock skew + a peer that was last
	// to receive our previous publish).
	seq := uint64(time.Now().UnixNano())
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	if got, err := node.Get(queryCtx, v.conf.PK, []byte("dmsg")); err == nil && got != nil && got.Seq >= seq {
		seq = got.Seq + 1
	}
	cancel()

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		dhtPublish(ctx, v, node, log, seq)
		seq++

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// dhtPublish writes the visor's DMSG discovery entry to the DHT under
// salt "dmsg".
//
// Transports are NOT published here — the TPDAdapter (wired into the
// transport manager via NewHybridTPDClient) is the sole writer of salt
// "tp". Two writers with different formats would race; the manager-
// driven path already publishes on every register/heartbeat in the
// canonical SignedEntry shape that GetTransportsByEdge reads back.
func dhtPublish(ctx context.Context, v *Visor, node *dht.Node, log *logging.Logger, seq uint64) {
	// Use the dmsg client's own discovery view (via DiscEntry → HTTP
	// discovery) rather than v.dClient. v.dClient is a direct.Client
	// returning synthetic placeholder entries with no Signature — those
	// are useful inside the visor for in-memory dialing but unsafe to
	// hand to the rest of the network as authoritative state.
	if v.dmsgC == nil {
		return
	}
	entry, err := v.dmsgC.DiscEntry(ctx, v.conf.PK)
	if err != nil || entry == nil {
		return
	}
	// Defense-in-depth: only publish a fully-formed signed entry. Even
	// if a future caller wires in a different discovery source, the DHT
	// can't reject malformed inner JSON on its own, so we filter here.
	if entry.Static != v.conf.PK || entry.Signature == "" {
		log.WithField("static_match", entry.Static == v.conf.PK).
			WithField("has_signature", entry.Signature != "").
			Debug("DHT: skipping publish of malformed self DMSG entry")
		return
	}
	data, err := json.Marshal(entry)
	if err != nil || len(data) > dht.MaxValueSize {
		return
	}
	if err := node.Put(ctx, data, seq, []byte("dmsg")); err != nil {
		log.WithError(err).Trace("DHT: publish DMSG entry failed")
	}
}
