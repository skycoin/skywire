// Package api pkg/deployment/conf/api/cxo_publisher.go c4-net-discovery
//
// CXO publisher for the deployment's services config: the same document
// GET / serves, gzipped JSON at deployment.ServicesCXOPath on
// DmsgConfCXOPort. The feed is signed by the conf service's own key, so a
// visor checks the config against that key rather than trusting whichever
// stream carried it, and the feed's sequence keeps an older config from
// being replayed.
//
// Visors hold one subscription to this feed (see pkg/visor/conf_cxo.go). The
// document changes rarely; the publisher's heartbeat keeps the connection
// warm in between, so a changed key reaches every subscribed visor within
// one publish interval and without a release.
//
// The feed is open: it carries exactly what GET / already serves to anyone.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// servicesPublishInterval is how often the publisher re-reads the served
// config. A publish happens only when the bytes changed, so this bounds how
// long a change (a config-file edit, a refreshed dmsg_servers list) takes to
// reach subscribers, not how often they are sent anything.
const servicesPublishInterval = time.Minute

// ServicesCXOPublisher publishes the API's services config as a CXO feed.
type ServicesCXOPublisher struct {
	api *API
	pub *treestore.Publisher
	log *logging.Logger

	cancel context.CancelFunc
	done   chan struct{}

	mu   sync.Mutex
	last []byte // the gzipped body last Put, to skip unchanged publishes
}

// StartServicesCXOPublisher starts publishing api's services config over
// dmsgC, as a feed signed by sk, until ctx ends or Close is called.
func StartServicesCXOPublisher(ctx context.Context, api *API, dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*ServicesCXOPublisher, error) {
	log := logging.MustGetLogger("conf-cxo-services-pub")
	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgConfCXOPort,
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil)

	pubCtx, cancel := context.WithCancel(ctx)
	sp := &ServicesCXOPublisher{
		api:    api,
		pub:    pub,
		log:    log,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgConfCXOPort).
			Info("CXO services publisher running")
	}
	go sp.loop(pubCtx)
	return sp, nil
}

// FeedPK returns the publisher's feed PK (the conf service's PK).
func (s *ServicesCXOPublisher) FeedPK() cipher.PubKey { return s.pub.Feed() }

// Close stops the publish loop and releases the publisher.
func (s *ServicesCXOPublisher) Close() error {
	s.cancel()
	<-s.done
	return s.pub.Close()
}

func (s *ServicesCXOPublisher) loop(ctx context.Context) {
	defer close(s.done)
	s.publishOnce()
	t := time.NewTicker(servicesPublishInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.publishOnce()
		}
	}
}

// publishOnce puts the current services config if it differs from what was
// last published. Put always marks the tree dirty, so the comparison is ours.
func (s *ServicesCXOPublisher) publishOnce() {
	svcs := s.api.Services()
	body, err := json.Marshal(&svcs)
	if err != nil {
		s.log.WithError(err).Warn("services marshal failed")
		return
	}
	gz := cxoutils.Gzip(body)

	s.mu.Lock()
	defer s.mu.Unlock()
	if bytes.Equal(gz, s.last) {
		return
	}
	if err := s.pub.Put(deployment.ServicesCXOPath, gz); err != nil {
		s.log.WithError(err).Warn("publisher Put failed")
		return
	}
	s.last = gz
}
