// Package visor pkg/visor/embedded_services_cxo.go c3-vis-core
//
// The visor as CXO host for the services it embeds. Visors publish their
// registration and telemetry feeds on fixed DMSG ports and a service
// aggregates each feed on the same port, dialing visors back there. A
// visor that embeds the service holds both ends under one key, and a port
// takes one listener, so the service's aggregator runs on the visor's own
// publisher node for that port. The visor's own feed on that node never
// arrives from a peer, so each Root it publishes is handed to the
// aggregator in-process.
package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	cxoregistry "github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// embeddedService is one config.embedded_services block, built once.
type embeddedService struct {
	block  services.Block
	label  string
	prefix string
	log    *logging.Logger
	svc    services.Embeddable
	err    error
	// standalone is set for a block with a key other than the visor's, or
	// one that cannot be mounted. It is rebuilt from ownRaw by factory on
	// every start and runs as its own service; ownPK is its key, if known.
	standalone bool
	ownPK      cipher.PubKey
	ownRaw     json.RawMessage
	factory    services.Factory
	// restarts counts how often an own-key service was started again.
	restarts int
	// current is the running own-key service instance, for its state.
	current services.Service
	// cancel ends the current run; stopped and restartNow are what the
	// operator asked for; wake ends a wait for a stopped or backing-off service.
	cancel     context.CancelFunc
	stopped    bool
	restartNow bool
	wake       chan struct{}
	// started is set once its runner is going, so a resume does not start a second.
	started bool
	// mu guards running (set once the service is mounted) and startErr,
	// written during init and read by state queries.
	mu       sync.Mutex
	running  bool
	startErr error
}

type embeddedSet struct {
	once sync.Once
	svcs []*embeddedService
	// board is the status page of svcs, built once.
	boardOnce sync.Once
	board     *charts.Board
	// ports are the CXO ports the embedded services aggregate on.
	ports map[uint16]bool
}

// embeddedServices builds the configured services once. It runs before any
// of them starts, from whichever needs it first: the publishers (to choose
// their storage) or the embedding module.
func (v *Visor) embeddedServices() []*embeddedService {
	v.embedded.once.Do(func() {
		// A block's log_level must not set the visor's own.
		services.SharedProcess()
		v.embedded.ports = map[uint16]bool{}
		seen := map[string]string{}
		for i, b := range v.conf.EmbeddedServices {
			es := &embeddedService{block: b, label: b.Label(), prefix: b.Prefix()}
			v.embedded.svcs = append(v.embedded.svcs, es)
			factory, ok := services.Lookup(b.Type)
			if !ok {
				es.err = fmt.Errorf("block #%d (%s): unknown type %q (registered: %v)",
					i, es.label, b.Type, services.RegisteredTypes())
				continue
			}
			raw, pk, hasKey, err := services.OwnKey(b.Raw)
			if err != nil {
				es.err = fmt.Errorf("block #%d (%s): key: %w", i, es.label, err)
				continue
			}
			es.log = v.MasterLogger().PackageLogger(es.label)
			if hasKey && pk != v.conf.PK {
				// Its own key: run as the standalone service would, inside
				// this process (init_embedded_services.go).
				es.setStandalone(pk, raw, factory)
				continue
			}
			svc, err := factory(b.Raw, es.log)
			if err != nil {
				es.err = fmt.Errorf("block #%d (%s): build: %w", i, es.label, err)
				continue
			}
			emb, ok := svc.(services.Embeddable)
			if !ok {
				if hasKey {
					es.err = fmt.Errorf("block #%d (%s): type %q cannot be mounted under the visor's key", i, es.label, b.Type)
					continue
				}
				// Keyless, or keyed in its own config file: run it as is.
				es.setStandalone(services.ConfigPubKey(raw), raw, factory)
				continue
			}
			if other, dup := seen[es.prefix]; dup {
				es.err = fmt.Errorf("block #%d (%s) and %s both mount at %s", i, es.label, other, es.prefix)
				continue
			}
			seen[es.prefix] = es.label
			es.svc = emb
			if agg, ok := svc.(services.CXOAggregating); ok {
				for _, p := range agg.AggregatorPorts() {
					v.embedded.ports[p] = true
				}
			}
		}
	})
	return v.embedded.svcs
}

func (es *embeddedService) setStandalone(pk cipher.PubKey, raw json.RawMessage, factory services.Factory) {
	es.standalone, es.ownPK, es.ownRaw, es.factory = true, pk, raw, factory
	es.wake = make(chan struct{}, 1)
}

// hostCXOPubStorage is cxoPubStorage for a publisher on port: in memory
// when an embedded service aggregates on that port. The service's
// aggregator then shares the publisher's node, and holds every visor's feed
// in that store, which the standalone services keep in memory too.
func (v *Visor) hostCXOPubStorage(dataDir string, port uint16) (string, bool) {
	v.embeddedServices()
	if v.embedded.ports[port] {
		return "", true
	}
	return cxoPubStorage(dataDir)
}

// trackCXOPublisher records the publisher on port for embedded services.
func (v *Visor) trackCXOPublisher(port uint16, pub *treestore.Publisher) {
	if pub == nil {
		return
	}
	v.cxoPubsMu.Lock()
	defer v.cxoPubsMu.Unlock()
	if v.cxoPubs == nil {
		v.cxoPubs = make(map[uint16]*treestore.Publisher)
	}
	v.cxoPubs[port] = pub
}

func (v *Visor) cxoPublisher(port uint16) *treestore.Publisher {
	v.cxoPubsMu.Lock()
	defer v.cxoPubsMu.Unlock()
	return v.cxoPubs[port]
}

// visorCXOHost implements services.CXOHost over the visor's publishers.
type visorCXOHost struct{ v *Visor }

func (h visorCXOHost) CXONode(port uint16) *node.Node {
	if pub := h.v.cxoPublisher(port); pub != nil {
		return pub.Node()
	}
	return nil
}

func (h visorCXOHost) OnLocalRoot(port uint16, fn func(*cxoregistry.Root)) {
	if pub := h.v.cxoPublisher(port); pub != nil {
		pub.SetPublishHook(fn)
	}
}

// embeddedServiceStates reports the embedded services for `visor state`.
func (v *Visor) embeddedServiceStates() []visorapi.EmbeddedServiceState {
	svcs := v.embeddedServices()
	if len(svcs) == 0 {
		return nil
	}
	out := make([]visorapi.EmbeddedServiceState, 0, len(svcs))
	for _, es := range svcs {
		st := visorapi.EmbeddedServiceState{
			Type:      es.block.Type,
			Name:      es.block.Name,
			URL:       fmt.Sprintf("dmsg://%s:%d%s", v.conf.PK.Hex(), visorconfig.DmsgHTTPPort, es.prefix),
			PlainHTTP: blockAddr(es.block),
		}
		if es.standalone {
			st.URL = ""
			if !es.ownPK.Null() {
				st.URL = "dmsg://" + es.ownPK.Hex()
			}
			st.OwnKey = true
		}
		es.mu.Lock()
		running, startErr, restarts, stopped := es.running, es.startErr, es.restarts, es.stopped
		es.mu.Unlock()
		st.Running = running
		st.Restarts = restarts
		st.Stopped = stopped
		switch {
		case es.err != nil:
			st.Error = es.err.Error()
		case startErr != nil:
			st.Error = startErr.Error()
		}
		var stater services.Service = es.svc
		if es.standalone {
			es.mu.Lock()
			stater = es.current
			es.mu.Unlock()
		}
		if s, ok := stater.(services.Stater); ok && running {
			st.State = s.State()
		}
		out = append(out, st)
	}
	return out
}
