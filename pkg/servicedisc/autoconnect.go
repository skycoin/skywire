package servicedisc

import (
	"context"
	"time"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
)

const (
	// PublicServiceDelay defines a delay before adding transports to public services.
	PublicServiceDelay = 5 * time.Second
)

// ConnectFn provides a way to connect to remote service
type ConnectFn func(context.Context, cipher.PubKey) error

// Autoconnector continuously tries to connect to services
type Autoconnector interface {
	Run(context.Context, ConnectFn) error
}

type autoconnector struct {
	client   *HTTPClient
	maxConns int
	log      *logging.Logger
}

// MakeConnector returns a new connector that will try to connect to at most maxConns
// services
func MakeConnector(conf Config, maxConns int, log *logging.Logger) Autoconnector {
	connector := &autoconnector{}
	connector.client = NewClient(log, conf)
	connector.maxConns = maxConns
	connector.log = log
	return connector
}

// Run implements Autoconnector interface
func (a *autoconnector) Run(ctx context.Context, connector ConnectFn) error {
	for {
		time.Sleep(PublicServiceDelay * 2)

		services, err := a.client.Services(ctx, a.maxConns)
		if err != nil {
			a.log.WithError(err).Errorln("Failed to fetch services")
			return err
		}

		for _, service := range services {
			pk := service.Addr.PubKey()
			err := connector(ctx, pk)
			if err != nil {
				// ignore for now?
			}
		}

		if len(services) == 0 {
			a.log.Println("no public services found")
		}
	}

}
