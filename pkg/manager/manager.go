// Package manager pkg/manager/manager.go
package manager

import (
	"context"
	"errors"
	"fmt"
	"net/rpc"

	"github.com/skycoin/dmsg/pkg/dmsg"
	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
)

// Manager provides an API that is wrapped in a authenticated RPC
type Manager struct {
	dmsgC   *dmsg.Client
	log     *logging.Logger
	tm      *transport.Manager
	tsNodes []cipher.PubKey
	localSK cipher.SecKey
}

// New makes a Manager from configuration
func New(ctx context.Context, pk cipher.PubKey, sk cipher.SecKey, tsnodes []cipher.PubKey, dmsgC *dmsg.Client, tm *transport.Manager, masterLogger *logging.MasterLogger) (*Manager, error) {
	log := masterLogger.PackageLogger("manager")
	log.WithField("local_pk", pk).Debug("Connecting to the dmsg network.")

	select {
	case <-dmsgC.Ready():
		log.WithField("local_pk", pk).Debug("Connected!")
		tl := &Manager{
			dmsgC:   dmsgC,
			log:     log,
			tm:      tm,
			tsNodes: tsnodes,
			localSK: sk,
		}
		return tl, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("failed to connect to dmsg network")
	}
}

// ListenAndServe listens for a dmsg connection and serves Management API rpc to trusted nodes
func (ts *Manager) ListenAndServe(ctx context.Context) {
	ts.log.WithField("dmsg_port", skyenv.DmsgTransportSetupPort).Debug("starting listener")
	lis, err := ts.dmsgC.Listen(skyenv.DmsgTransportSetupPort)
	if err != nil {
		ts.log.WithError(err).Error("failed to listen")
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			ts.log.WithError(err).Warn("Dmsg listener closed with non-nil error.")
		}
	}()

	ts.log.WithField("dmsg_port", skyenv.DmsgTransportSetupPort).Debug("Accepting dmsg streams.")
	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			log := ts.log.WithError(err)
			if errors.Is(err, dmsg.ErrEntityClosed) {
				log.Debug("Dmsg client stopped serving.")
				break
			}
			log.Error("Failed to accept")
			break
		}
		remotePK := conn.RawRemoteAddr().PK
		found := false
		for _, trusted := range ts.tsNodes {
			if trusted == remotePK {
				found = true
				break
			}
		}

		if !found {
			ts.log.WithField("remote_conn", remotePK).WithField("trusted", ts.tsNodes).Debug("Closing connection")
			if err := conn.Close(); err != nil {
				ts.log.WithError(err).Error("Failed to close stream")
			}
		}

		sharedSec, err := skycipher.ECDH(skycipher.PubKey(remotePK), skycipher.SecKey(ts.localSK))
		if err != nil {
			ts.log.WithError(err).Error("failed to created ECDH")
		}

		gw := &RPC{tm: ts.tm, log: ts.log, sharedSec: sharedSec}
		rpcS := rpc.NewServer()

		if err := rpcS.Register(gw); err != nil {
			ts.log.WithError(err).Error("failed to register rpc")
		}
		ts.log.WithField("remote_conn", remotePK).Debug("Serving rpc")
		go rpcS.ServeConn(conn)
	}
}
