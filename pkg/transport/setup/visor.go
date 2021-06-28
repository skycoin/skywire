package setup

import (
	"context"
	"fmt"
	"net/rpc"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

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
	log.WithField("local_pk", conf.PK).Info("Connecting to the dmsg network.")

	go dmsgC.Serve(ctx)

	select {
	case <-dmsgC.Ready():
		log.WithField("local_pk", conf.PK).Info("Connected!")
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
	ts.log.WithField("dmsg_port", skyenv.DmsgTransportSetupPort).Info("starting listener")
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

	ts.log.WithField("dmsg_port", skyenv.DmsgTransportSetupPort).Info("Accepting dmsg streams.")
	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			ts.log.WithError(err).Error("failed to accept")
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
			ts.log.WithField("remote_conn", remotePK).WithField("trusted", ts.tsNodes).Info("Closing connection")
			if err := conn.Close(); err != nil {
				ts.log.WithError(err).Error("Failed to close stream")
			}
		}
		gw := &TransportGateway{tm: ts.tm, log: ts.log}
		rpcS := rpc.NewServer()
		if err := rpcS.Register(gw); err != nil {
			ts.log.WithError(err).Error("failed to register rpc")
		}
		ts.log.WithField("remote_conn", remotePK).Info("Serving rpc")
		go rpcS.ServeConn(conn)
	}
}
