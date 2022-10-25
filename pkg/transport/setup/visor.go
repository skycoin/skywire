// Package setup pkg/transport/setup/visor.go
package setup

import (
	"context"
	"errors"
	"fmt"
	"net/rpc"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TransportListener provides an rpc service over dmsg to perform skycoin transport setup
type TransportListener struct {
	dmsgC   *dmsg.Client
	log     *logging.Logger
	tm      *transport.Manager
	tsNodes []cipher.PubKey
}

// NewTransportListener makes a TransportListener from configuration
func NewTransportListener(ctx context.Context, conf *visorconfig.V1, dmsgC *dmsg.Client, tm *transport.Manager, masterLogger *logging.MasterLogger) (*TransportListener, error) {
	log := masterLogger.PackageLogger("transport_setup")
	log.WithField("local_pk", conf.PK).Debug("Connecting to the dmsg network.")

	select {
	case <-dmsgC.Ready():
		log.WithField("local_pk", conf.PK).Debug("Connected!")
		tl := &TransportListener{
			dmsgC:   dmsgC,
			log:     log,
			tm:      tm,
			tsNodes: conf.Transport.TransportSetup,
		}
		return tl, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("failed to connect to dmsg network")
	}
}

// Serve transport setup rpc to trusted nodes over dmsg
func (ts *TransportListener) Serve(ctx context.Context) {
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
		gw := &TransportGateway{tm: ts.tm, log: ts.log}
		rpcS := rpc.NewServer()
		if err := rpcS.Register(gw); err != nil {
			ts.log.WithError(err).Error("failed to register rpc")
		}
		ts.log.WithField("remote_conn", remotePK).Debug("Serving rpc")
		go rpcS.ServeConn(conn)
	}
}
