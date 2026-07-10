// Package visor pkg/visor/init_coinnode.go
//
// Brings up fibercoin node discovery (servicedisc.ServiceTypeCoin). For each
// v.conf.CoinNodes entry the visor:
//
//  1. reverse-proxies the node's local HTTP API over dmsg on the configured
//     DmsgPort (reusing the forwarded-ports infra), so a remote thin-client
//     wallet — including the browser wasm visor over its HTTP-over-dmsg shim —
//     can reach the node's API through the mesh; and
//  2. on the SD heartbeat interval, probes the node's /api/v1/health, builds a
//     DETECTED CoinInfo (never operator-claimed), and registers a type=coin SD
//     entry ONLY while the node answers — deregistering when it goes away, so
//     the advertisement is always truthful (no dead entries).
//
// The node itself runs INDEPENDENTLY of the visor (systemd/terminal); the visor
// never starts or stops it. See docs and project_skycoin_wallet_over_dmsg.
package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// coinHealthProbeTimeout bounds a single /health probe. Kept short so a hung
// node can't stall the heartbeat loop.
const coinHealthProbeTimeout = 10 * time.Second

// coinHealth is the subset of the skycoin /api/v1/health response we read to
// build a CoinInfo. Every field is generic across fibercoins (skycoin, mdl,
// aviate, …) — they share the daemon codebase and this response shape.
type coinHealth struct {
	Coin             string `json:"coin"`
	BlockchainPubkey string `json:"blockchain_pubkey"`
	Version          struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"version"`
	Fiber struct {
		Name string `json:"name"`
	} `json:"fiber"`
	Blockchain struct {
		Head struct {
			Seq uint64 `json:"seq"`
		} `json:"head"`
	} `json:"blockchain"`
}

// parseCoinHealth maps a raw /api/v1/health JSON body to a CoinInfo. The coin
// name prefers fiber.name (canonical) and falls back to the top-level "coin"
// field. Returns an error on malformed JSON so a bad response never registers a
// bogus entry.
func parseCoinHealth(body []byte) (*servicedisc.CoinInfo, error) {
	var h coinHealth
	if err := json.Unmarshal(body, &h); err != nil {
		return nil, fmt.Errorf("parse coin health: %w", err)
	}
	fiber := h.Fiber.Name
	if fiber == "" {
		fiber = h.Coin
	}
	return &servicedisc.CoinInfo{
		BlockchainPubKey: h.BlockchainPubkey,
		Fiber:            fiber,
		Version:          h.Version.Version,
		Commit:           h.Version.Commit,
		HeadSeq:          h.Blockchain.Head.Seq,
	}, nil
}

// probeCoinNode GETs <localAddr>/api/v1/health and parses it into a CoinInfo.
// localAddr is the node's HTTP API address on this host (e.g. 127.0.0.1:6420).
func probeCoinNode(ctx context.Context, client *http.Client, localAddr string) (*servicedisc.CoinInfo, error) {
	url := "http://" + localAddr + "/api/v1/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coin node health: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseCoinHealth(body)
}

// initCoinNodes wires fibercoin node discovery. Best-effort: no configured
// nodes, or no dmsg client, is a no-op. Each node gets a dmsg forward and a
// health-gated SD registrar loop.
func initCoinNodes(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.conf == nil || len(v.conf.CoinNodes) == 0 {
		return nil
	}
	if v.dmsgC == nil {
		log.Warn("coin_nodes configured but dmsg client is nil; skipping")
		return nil
	}

	// The SD address must be a dmsg:// URL — the entry we register is reachable
	// over the mesh, so it's registered through the dmsg-served SD.
	sdURL := v.conf.Launcher.ServiceDiscDmsg
	if sdURL == "" {
		sdURL = v.conf.Launcher.ServiceDisc
	}
	httpC, err := getHTTPClient(context.Background(), v, sdURL)
	if err != nil {
		log.WithError(err).Warn("coin_nodes: cannot build SD dmsg client; skipping")
		return nil
	}

	// Local health probes are plain localhost HTTP (the node's own API); no
	// dmsg involved. Short per-probe timeout via the request context.
	localHTTP := &http.Client{}

	ctx, cancel := context.WithCancel(context.Background())
	for i := range v.conf.CoinNodes {
		node := v.conf.CoinNodes[i]
		if node.LocalAddr == "" || node.DmsgPort == 0 {
			log.WithField("index", i).Warn("coin_nodes: entry missing local_addr or dmsg_port; skipping")
			continue
		}

		// (1) Expose the node's local API over dmsg on DmsgPort. ProxyAddr fronts
		// the external local endpoint; LocalPort 0 means "use ProxyAddr".
		fp := ForwardedPort{
			Port:      int(node.DmsgPort),
			ProxyAddr: node.LocalAddr,
			Label:     "coin",
			DMSG:      true,
			Whitelist: node.Whitelist,
		}
		if rerr := v.forwardedPorts.Register(fp); rerr != nil {
			log.WithError(rerr).WithField("dmsg_port", node.DmsgPort).
				Warn("coin_nodes: failed to register forwarded port; skipping")
			continue
		}
		v.startDmsgForwarder(int(node.DmsgPort), 0)

		// (2) Health-gated SD registrar for this node.
		sdClient := servicedisc.NewClient(
			logging.MustGetLogger("servicedisc.coin"),
			newInfoMasterLogger(),
			servicedisc.Config{
				Type:     servicedisc.ServiceTypeCoin,
				PK:       v.conf.PK,
				SK:       v.conf.SK,
				Port:     node.DmsgPort,
				DiscAddr: sdURL,
			}, httpC, "")

		go runCoinRegistrar(ctx, log.WithField("dmsg_port", node.DmsgPort), sdClient, localHTTP, node)
	}

	v.pushCloseStack("coin_nodes", func() error { cancel(); return nil })
	log.WithField("count", len(v.conf.CoinNodes)).Info("coin_nodes: fibercoin node discovery up")
	return nil
}

// newInfoMasterLogger builds an info-level master logger for a coin SD client,
// matching the convention used by the on-demand services lookup path.
func newInfoMasterLogger() *logging.MasterLogger {
	ml := logging.NewMasterLogger()
	ml.SetLevel(logrus.InfoLevel)
	return ml
}

// runCoinRegistrar probes the node's /health on the SD heartbeat interval and
// keeps the type=coin entry registered only while the node answers. It probes
// once immediately, then on each tick. On a failed probe it deregisters (the
// node is down / the advertisement would be dead). CoinInfo is refreshed every
// successful probe so volatile fields (HeadSeq) stay current.
func runCoinRegistrar(ctx context.Context, log logrus.FieldLogger, sd *servicedisc.HTTPClient, localHTTP *http.Client, node visorconfig.CoinNodeConfig) {
	ticker := time.NewTicker(skyenv.ServiceDiscUpdateInterval)
	defer ticker.Stop()

	registered := false
	tick := func() {
		pctx, cancel := context.WithTimeout(ctx, coinHealthProbeTimeout)
		defer cancel()
		info, err := probeCoinNode(pctx, localHTTP, node.LocalAddr)
		if err != nil {
			if registered {
				log.WithError(err).Info("coin node unhealthy; deregistering")
				if derr := sd.DeleteEntry(ctx); derr != nil {
					log.WithError(derr).Debug("coin node deregister failed")
				}
				registered = false
			}
			return
		}
		sd.SetCoinInfo(info)
		if rerr := sd.RegisterEntry(ctx); rerr != nil {
			log.WithError(rerr).Debug("coin node register failed")
			return
		}
		if !registered {
			log.WithField("fiber", info.Fiber).WithField("head_seq", info.HeadSeq).
				Info("coin node healthy; registered type=coin")
			registered = true
		}
	}

	tick()
	for {
		select {
		case <-ctx.Done():
			if registered {
				// ctx is already canceled here (shutdown), so derive a live,
				// non-cancelable context from it for the final deregister —
				// keeps values, drops the cancellation, no bare Background.
				dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coinHealthProbeTimeout)
				_ = sd.DeleteEntry(dctx) //nolint:errcheck
				cancel()
			}
			return
		case <-ticker.C:
			tick()
		}
	}
}
