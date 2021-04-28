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

// CheckConnFN checks that connection is alive
type CheckConnFN func(cipher.PubKey) bool

// Autoconnector continuously tries to connect to services
type Autoconnector interface {
	Run(context.Context, ConnectFn, CheckConnFN) error
}

type autoconnector struct {
	client   *HTTPClient
	maxConns int
	log      *logging.Logger
	conns    map[cipher.PubKey]struct{}
}

// MakeConnector returns a new connector that will try to connect to at most maxConns
// services
func MakeConnector(conf Config, maxConns int, log *logging.Logger) Autoconnector {
	connector := &autoconnector{}
	connector.client = NewClient(log, conf)
	connector.maxConns = maxConns
	connector.log = log
	connector.conns = make(map[cipher.PubKey]struct{})
	return connector
}

// Run implements Autoconnector interface
func (a *autoconnector) Run(ctx context.Context, connector ConnectFn, checker CheckConnFN) error {
	for {
		time.Sleep(PublicServiceDelay * 2)
		a.checkConns(checker)
		if len(a.conns) == a.maxConns {
			continue
		}
		services, err := a.client.Services(ctx, a.maxConns)
		if err != nil {
			// todo: exponential backoff
			a.log.WithError(err).Errorln("Failed to fetch services")
			continue
		}

		for _, service := range services {
			pk := service.Addr.PubKey()
			if _, ok := a.conns[pk]; ok {
				continue
			}
			err := connector(ctx, pk)
			if err != nil {
				// ignore for now?
			} else {
				a.conns[pk] = struct{}{}
			}
		}
	}
}

// check if existing connections are still active using checker
// and delete those that are not
func (a *autoconnector) checkConns(checker CheckConnFN) {
	toDelete := make([]cipher.PubKey, 0)
	for pk := range a.conns {
		if !checker(pk) {
			toDelete = append(toDelete, pk)
		}
	}
	for _, pk := range toDelete {
		delete(a.conns, pk)
	}
}
