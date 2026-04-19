package visor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/dht"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

func initDHT(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.DHT

	// DHT is enabled by default when DMSG is available.
	// Set dht.enable=false to explicitly disable.
	if conf != nil && !conf.Enable {
		return nil
	}

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

	tp := dht.NewDMSGTransport(v.dmsgC)
	node := dht.New(dhtCfg, v.conf.PK, v.conf.SK, tp, log)

	if err := node.Start(ctx); err != nil {
		return err
	}

	v.initLock.Lock()
	v.dhtNode = node
	v.initLock.Unlock()

	v.pushCloseStack("dht", func() error {
		return node.Stop()
	})

	log.WithField("id", node.ID().String()[:16]).
		WithField("bootstrap_peers", len(bootstrapPKs)).
		WithField("full_node", fullNode).
		Info("DHT node started")

	// Start background dual-write: publish visor's DMSG entry and
	// transport list to the DHT alongside the normal HTTP updates.
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
