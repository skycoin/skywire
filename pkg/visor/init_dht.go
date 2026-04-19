package visor

import (
	"context"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/dht"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

func initDHT(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.DHT

	// DHT is enabled by default when DMSG is available.
	// Set dht.enable=false to explicitly disable.
	if conf != nil && !conf.Enable {
		return nil
	}

	if v.dmsgC == nil {
		return nil // no DMSG client, skip silently
	}

	// Wait for DMSG to be ready before starting DHT.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-v.dmsgC.Ready():
	}

	// Use deployment service PKs as bootstrap peers if none configured.
	var bootstrapPKs []cipher.PubKey
	var fullNode bool
	var whitelistedPKs, trustedPKs []cipher.PubKey

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
		WithField("bootstrap_peers", len(conf.BootstrapPKs)).
		WithField("full_node", conf.FullNode).
		Info("DHT node started")

	return nil
}
