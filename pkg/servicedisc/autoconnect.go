package servicedisc

import (
	"context"
	"time"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/internal/netutil"
	"github.com/skycoin/skywire/pkg/snet/directtp/tptypes"
	"github.com/skycoin/skywire/pkg/transport"
)

const (
	// PublicServiceDelay defines a delay before adding transports to public services.
	PublicServiceDelay = 10 * time.Second

	fetchServicesDelay = 2 * time.Second
)

// ConnectFn provides a way to connect to remote service
type ConnectFn func(context.Context, cipher.PubKey) error

// Autoconnector continuously tries to connect to services
type Autoconnector interface {
	Run(context.Context) error
}

type autoconnector struct {
	client   *HTTPClient
	maxConns int
	log      *logging.Logger
	conns    map[cipher.PubKey]struct{}
	tm       *transport.Manager
}

// MakeConnector returns a new connector that will try to connect to at most maxConns
// services
func MakeConnector(conf Config, maxConns int, tm *transport.Manager, log *logging.Logger) Autoconnector {
	connector := &autoconnector{}
	connector.client = NewClient(log, conf)
	connector.maxConns = maxConns
	connector.log = log
	connector.conns = make(map[cipher.PubKey]struct{})
	connector.tm = tm
	return connector
}

// Run implements Autoconnector interface
func (a *autoconnector) Run(ctx context.Context) error {
	retrier := netutil.NewRetrier(fetchServicesDelay, 0, 2)
	for {
		time.Sleep(PublicServiceDelay)
		a.checkConns()
		if len(a.conns) == a.maxConns {
			continue
		}
		var services []Service
		fetch := func() (err error) {
			// "return" services up from the closure
			services, err = a.client.Services(ctx, a.maxConns)
			if err != nil {
				return err
			}
			return nil
		}
		if err := retrier.Do(fetch); err != nil {
			a.log.Errorf("Cannot fetch services: %s", err)
			return err
		}

		for _, service := range services {
			pk := service.Addr.PubKey()
			if _, ok := a.conns[pk]; ok {
				continue
			}
			a.connect(ctx, pk)
		}
	}
}

// check if existing connections are still active using checker
// and delete those that are not
func (a *autoconnector) checkConns() {
	toDelete := make([]cipher.PubKey, 0)
	for pk := range a.conns {
		if !a.checkConn(pk) {
			toDelete = append(toDelete, pk)
		}
	}
	for _, pk := range toDelete {
		delete(a.conns, pk)
	}
}

func (a *autoconnector) checkConn(pk cipher.PubKey) bool {
	t, err := a.tm.GetTransport(pk, tptypes.STCPR)
	if err != nil {
		return false
	}
	up := t.IsUp()
	if !up {
		a.tm.DeleteTransport(t.Entry.ID)
	}
	return up
}

func (a *autoconnector) connect(ctx context.Context, pk cipher.PubKey) {
	a.log.WithField("pk", pk).Infoln("Adding transport to public visor")
	if _, err := a.tm.SaveTransport(ctx, pk, tptypes.STCPR); err != nil {
		a.log.
			WithError(err).
			WithField("pk", pk).
			WithField("type", tptypes.STCPR).
			Warnln("Failed to add transport to public visor")
		return
	}
	a.log.
		WithField("pk", pk).
		WithField("type", tptypes.STCPR).
		Infoln("Added transport to public visor")
	a.conns[pk] = struct{}{}
}
