package visor

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/dht"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
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

	log.WithField("id", node.ID().String()[:16]).
		WithField("bootstrap_peers", len(bootstrapPKs)).
		WithField("full_node", fullNode).
		Info("DHT node started")

	// Register full DHT nodes in service discovery so other visors
	// can discover them as additional bootstrap peers.
	if fullNode {
		go dhtRegisterFullNode(ctx, v, log) //nolint:gosec
	}

	// Wrap discovery clients with DHT hybrid clients so reads try
	// DHT first, fall back to HTTP. Writes go to both.
	discAdapter := dht.NewDiscAdapter(node, log)
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
	go dhtPublishLoop(ctx, v, node, log)

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

	seq := uint64(1)
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

// dhtRegisterFullNode registers this visor as a DHT full node in
// service discovery so other visors can discover it as a bootstrap peer.
func dhtRegisterFullNode(ctx context.Context, v *Visor, log *logging.Logger) {
	sdAddr := ""
	if v.conf.Launcher != nil && v.conf.Launcher.ServiceDisc != "" {
		sdAddr = v.conf.Launcher.ServiceDisc
	}
	if sdAddr == "" {
		log.Warn("DHT full node: no service discovery address; skipping registration")
		return
	}

	sdConf := servicedisc.Config{
		Type:     servicedisc.ServiceTypeDHTNode,
		PK:       v.conf.PK,
		SK:       v.conf.SK,
		Port:     100, // DHT DMSG port
		DiscAddr: sdAddr,
	}
	sdClient := servicedisc.NewClient(log, logging.NewMasterLogger(), sdConf, &http.Client{}, "")

	// Initial registration.
	if err := sdClient.Register(ctx); err != nil {
		log.WithError(err).Warn("DHT full node: initial SD registration failed")
	} else {
		log.Info("DHT full node registered in service discovery")
	}

	// Periodic heartbeat (same interval as other services: 90s).
	ticker := time.NewTicker(90 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			sdClient.DeleteEntry(context.Background()) //nolint:errcheck,gosec
			return
		case <-ticker.C:
			if err := sdClient.Register(ctx); err != nil {
				log.WithError(err).Trace("DHT full node: SD heartbeat failed")
			}
		}
	}
}

func dhtPublish(ctx context.Context, v *Visor, node *dht.Node, log *logging.Logger, seq uint64) {
	// Publish DMSG discovery entry.
	if v.dClient != nil {
		entry, err := v.dClient.Entry(ctx, v.conf.PK)
		if err == nil && entry != nil {
			data, err := json.Marshal(entry)
			if err == nil && len(data) <= dht.MaxValueSize {
				if err := node.Put(ctx, data, seq, []byte("dmsg")); err != nil {
					log.WithError(err).Trace("DHT: publish DMSG entry failed")
				}
			}
		}
	}

	// Publish transport list.
	if v.tpM != nil {
		tps := v.tpM.GetTransportsByLabels(transport.LabelSkycoin, transport.LabelAutomatic)
		if len(tps) > 0 {
			type tpEntry struct {
				Remote  cipher.PubKey `json:"r"`
				Type    string        `json:"t"`
				Latency float64       `json:"l,omitempty"`
			}
			entries := make([]tpEntry, 0, len(tps))
			for _, tp := range tps {
				if tp.IsClosed() {
					continue
				}
				entries = append(entries, tpEntry{
					Remote:  tp.Remote(),
					Type:    string(tp.Type()),
					Latency: tp.GetLatency(),
				})
			}
			data, err := json.Marshal(entries)
			if err == nil && len(data) <= dht.MaxValueSize {
				if err := node.Put(ctx, data, seq, []byte("tp")); err != nil {
					log.WithError(err).Trace("DHT: publish transports failed")
				}
			}
		}
	}
}
