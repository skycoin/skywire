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
	tm       *transport.Manager
}

// MakeConnector returns a new connector that will try to connect to at most maxConns
// services
func MakeConnector(conf Config, maxConns int, tm *transport.Manager, log *logging.Logger) Autoconnector {
	connector := &autoconnector{}
	connector.client = NewClient(log, conf)
	connector.maxConns = maxConns
	connector.log = log
	connector.tm = tm
	return connector
}

// Run implements Autoconnector interface
func (a *autoconnector) Run(ctx context.Context) error {
	for {
		time.Sleep(PublicServiceDelay)
		a.log.Infof("Fetching public visors")
		addresses, err := a.fetchPubAddresses(ctx)
		if err != nil {
			a.log.Errorf("Cannot fetch public services: %s", err)
		}

		trs := a.updateTransports()
		absent := a.calculateAbsent(addresses, trs)
		for _, pk := range absent {
			a.log.WithField("pk", pk).Infoln("Adding transport to public visor")
			logger := a.log.WithField("pk", pk).WithField("type", tptypes.STCPR)
			if _, err := a.tm.SaveTransport(ctx, pk, tptypes.STCPR, transport.LabelAutomatic); err != nil {
				logger.WithError(err).Warnln("Failed to add transport to public visor")
				continue
			}
			logger.Infoln("Added transport to public visor")
		}
	}
}

// Remove all inactive automatic transports and return all active
// automatic transports
func (a *autoconnector) updateTransports() []*transport.ManagedTransport {
	trs := a.tm.GetTransportsByLabel(transport.LabelAutomatic)
	var trsActive []*transport.ManagedTransport
	for _, tr := range trs {
		if !tr.IsUp() {
			a.tm.DeleteTransport(tr.Entry.ID)
		} else {
			trsActive = append(trsActive, tr)
		}
	}
	return trsActive
}

func (a *autoconnector) fetchPubAddresses(ctx context.Context) ([]cipher.PubKey, error) {
	retrier := netutil.NewRetrier(fetchServicesDelay, 0, 2)
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
		return nil, err
	}
	var pkeys []cipher.PubKey
	for _, service := range services {
		pkeys = append(pkeys, service.Addr.PubKey())
	}
	return pkeys, nil
}

func (a *autoconnector) calculateAbsent(pkeys []cipher.PubKey, trs []*transport.ManagedTransport) []cipher.PubKey {
	var absent []cipher.PubKey
	for _, pk := range pkeys {
		found := false
		for _, tr := range trs {
			if tr.Entry.HasEdge(pk) {
				found = true
				break
			}
		}
		if !found {
			absent = append(absent, pk)
		}
	}
	return absent
}
