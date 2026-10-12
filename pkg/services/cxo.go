// Package services pkg/services/cxo.go c2-vis-appsvc
package services

import (
	"context"
	"sync"

	"github.com/skycoin/skywire/pkg/cxo/cxoaggregate"
	"github.com/skycoin/skywire/pkg/cxo/node"
	cxoregistry "github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
)

// Aggregator is a CXO fan-in aggregator (the cxoaggregate-based ones in
// dmsg-discovery, the address resolver, service discovery and transport
// discovery all satisfy it).
type Aggregator interface {
	Run(ctx context.Context)
	Close() error
	Stats() cxoaggregate.Stats
	Ingest(r *cxoregistry.Root)
}

// CXOSet is one service's CXO aggregators and publishers. It starts them
// the same way in every service, on the host's nodes where the host lends
// them, closes them when the service stops, and reports them in State.
type CXOSet struct {
	// Stats, when set, names each feed's port on the status page.
	Stats interface {
		NamePort(port uint16, name string)
	}

	mu   sync.Mutex
	aggs []cxoAgg
	pubs []cxoPub
}

type cxoAgg struct {
	name   string
	port   uint16
	agg    Aggregator
	shared bool
	err    string
}

// Publisher is a service's CXO feed publisher wrapper.
type Publisher interface {
	Close() error
	Publisher() *treestore.Publisher
}

type cxoPub struct {
	name string
	port uint16
	pub  *treestore.Publisher
	err  string
}

// StartAggregator builds the aggregator for port with build, which gets the
// host's node for that port or nil (build its own). It runs until ctx ends.
// On a lent node the host's own Roots on that port are fed to it, since a
// node never receives its own feed from a peer. A failure is logged and
// recorded; the service carries on without that aggregator.
func (s *CXOSet) StartAggregator(ctx context.Context, host CXOHost, log *logging.Logger,
	name string, port uint16, build func(n *node.Node) (Aggregator, error)) {
	if s.Stats != nil {
		s.Stats.NamePort(port, "cxo "+name+" (in)")
	}
	var n *node.Node
	if host != nil {
		n = host.CXONode(port)
	}
	agg, err := build(n)
	rec := cxoAgg{name: name, port: port, shared: n != nil}
	if err != nil {
		rec.err = err.Error()
		log.WithError(err).WithField("port", port).Errorf("CXO %s aggregator failed to start; continuing without it", name)
		s.add(rec)
		return
	}
	rec.agg = agg
	agg.Run(ctx)
	if n != nil {
		host.OnLocalRoot(port, agg.Ingest)
	}
	go func() {
		<-ctx.Done()
		_ = agg.Close() //nolint:errcheck
	}()
	log.WithField("port", port).WithField("shared", n != nil).Infof("CXO %s aggregator running", name)
	s.add(rec)
}

// AddPublisher records a publisher started by the service, and closes it
// when ctx ends. err, if set, is recorded and logged instead.
func (s *CXOSet) AddPublisher(ctx context.Context, log *logging.Logger, name string, port uint16,
	pub Publisher, err error) {
	if s.Stats != nil {
		s.Stats.NamePort(port, "cxo "+name)
	}
	rec := cxoPub{name: name, port: port}
	if err != nil {
		rec.err = err.Error()
		log.WithError(err).WithField("port", port).Errorf("CXO %s publisher failed to start; continuing without it", name)
	} else {
		rec.pub = pub.Publisher()
		go func() {
			<-ctx.Done()
			_ = pub.Close() //nolint:errcheck
		}()
	}
	s.mu.Lock()
	s.pubs = append(s.pubs, rec)
	s.mu.Unlock()
}

func (s *CXOSet) add(a cxoAgg) {
	s.mu.Lock()
	s.aggs = append(s.aggs, a)
	s.mu.Unlock()
}

// State fills st's CXO fields.
func (s *CXOSet) State(st *State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.aggs {
		as := AggregatorState{Name: a.name, Port: a.port, Shared: a.shared, Error: a.err}
		if a.agg != nil {
			x := a.agg.Stats()
			as.Conns, as.Subscribed, as.Feeds, as.LocalRoots = x.Conns, x.Subscribed, x.Feeds, x.LocalRoots
		}
		st.Aggregators = append(st.Aggregators, as)
	}
	for _, p := range s.pubs {
		ps := PublisherState{Name: p.name, Port: p.port}
		if p.pub != nil {
			ps.PublishState = p.pub.PublishState()
			ps.PublisherStats = p.pub.Stats()
		} else {
			ps.LastErr = p.err
		}
		st.Publishers = append(st.Publishers, ps)
	}
}

// CXOAggregating is implemented by services that aggregate CXO feeds. A
// host that embeds one keeps its own publishers on these ports in memory
// and lends them to the service (see CXOHost).
type CXOAggregating interface {
	AggregatorPorts() []uint16
}
