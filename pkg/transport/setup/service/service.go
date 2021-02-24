package service

import (
	"context"
	"fmt"
	"net/rpc"
	"time"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/disc"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// TransportSetup is a wrapper around dmsg client that provides an rpc service
// over dmsg to perform skycoin transport setup
type TransportSetup struct {
	dmsgC *dmsg.Client
	log   *logging.Logger
	conf  Config
}

// NewTransportSetup makes a TransportSetup from configuration
func NewTransportSetup(ctx context.Context, conf Config, log *logging.Logger) (*TransportSetup, error) {
	disc := disc.NewHTTP(conf.Dmsg.Discovery)
	dmsgConf := &dmsg.Config{MinSessions: conf.Dmsg.SessionsCount}
	dmsgC := dmsg.NewClient(conf.PK, conf.SK, disc, dmsgConf)
	go dmsgC.Serve(ctx)
	log.WithField("local_pk", conf.PK).WithField("dmsg_conf", conf.Dmsg).
		Info("Connecting to the dmsg network.")
	select {
	case <-dmsgC.Ready():
		log.Info("Connected!")
		return &TransportSetup{dmsgC: dmsgC, log: log, conf: conf}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("failed to connect to dmsg network")
	}
}

func (ts *TransportSetup) Serve(ctx context.Context) error {
	const dmsgPort = skyenv.DmsgTransportSetupServicePort
	const timeout = 30 * time.Second
	ts.log.WithField("dmesg_port", dmsgPort).Info("starting listener")
	lis, err := ts.dmsgC.Listen(dmsgPort)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			ts.log.WithError(err).Warn("Dmsg listener closed with non-nil error.")
		}
	}()

	ts.log.WithField("dmsg_port", dmsgPort).Info("Accepting dmsg streams.")
	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			return err
		}
		gw := &TestGateway{}
		rpcS := rpc.NewServer()
		if err := rpcS.Register(gw); err != nil {
			return err
		}
		ts.log.WithField("remote_conn", conn.RawRemoteAddr().PK).Info("Serving rpc")
		go rpcS.ServeConn(conn)
	}
}
