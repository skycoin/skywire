package pty

import (
	"context"
	"encoding/json"
	"net"
	"net/rpc"
	"sync"

	"github.com/SkycoinProject/skywire-mainnet/pkg/dmsgpty/ptycfg"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/sirupsen/logrus"
)

// Server represents the dmsgpty-server.
type Server struct {
	log logrus.FieldLogger

	pk   cipher.PubKey
	sk   cipher.SecKey
	auth ptycfg.Whitelist
}

// NewServer instantiates a dmsgpty-server.
func NewServer(log logrus.FieldLogger, pk cipher.PubKey, sk cipher.SecKey, authFile string) (*Server, error) {
	if log == nil {
		log = logging.MustGetLogger("dmsgpty-server")
	}

	auth, err := ptycfg.NewJSONFileWhiteList(authFile)
	if err != nil {
		return nil, err
	}

	authAll, err := auth.All()
	if err != nil {
		return nil, err
	}

	authStr, _ := json.MarshalIndent(authAll, "", "\t") //nolint:errcheck

	log.Info("whitelist:", string(authStr))

	return &Server{
		log:  log,
		pk:   pk,
		sk:   sk,
		auth: auth,
	}, nil
}

// Auth returns the internal whitelist used by dmsgpty-server.
func (s *Server) Auth() ptycfg.Whitelist { return s.auth }

// Serve serves the dmsgpty-server for remote requests over dmsg.
func (s *Server) Serve(ctx context.Context, lis *dmsg.Listener) {
	wg := new(sync.WaitGroup)
	defer wg.Wait()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for {
		st, err := lis.AcceptStream()
		if err != nil {
			if err, ok := err.(net.Error); ok && err.Temporary() {
				s.log.WithError(err).Warn("acceptTransport temporary error.")
				continue
			}
			s.log.WithError(err).Warn("acceptTransport error.")
			return
		}

		remote := st.RemoteAddr().(dmsg.Addr)
		log := s.log.WithField("remote_pk", remote.PK)
		log.Info("received request")

		ok, err := s.auth.Get(remote.PK)
		if err != nil {
			log.WithError(err).Error("dmsgpty-server whitelist error")
			return
		}
		if !ok {
			log.Warn("rejected by whitelist")
			if err := st.Close(); err != nil {
				log.WithError(err).Warn("close transport error")
			}

			continue
		}

		log.Info("request accepted")
		wg.Add(1)

		go func(st *dmsg.Stream) {
			done := make(chan struct{})
			defer func() {
				close(done)
				wg.Done()
			}()
			go func() {
				select {
				case <-done:
				case <-ctx.Done():
					_ = st.Close() //nolint:errcheck
				}
			}()
			s.handleConn(log, remote.PK, st)
		}(st)
	}
}

// handles connection (assumes remote party is authorized to connect).
func (s *Server) handleConn(log logrus.FieldLogger, _ cipher.PubKey, conn net.Conn) {

	// TODO(evanlinjin): Wrap connection with noise.
	//ns, err := noise.New(noise.HandshakeXK, noise.Config{
	//	LocalPK:   s.conf.PK,
	//	LocalSK:   s.conf.SK,
	//	RemotePK:  rPK,
	//	Initiator: false,
	//})
	//if err != nil {
	//	log.WithError(err).Fatal("handleConn: failed to init noise")
	//}
	//conn, err = noise.WrapConn(conn, ns, noise.AcceptHandshakeTimeout)
	//if err != nil {
	//	log.WithError(err).Warn("handleConn: noise handshake failed")
	//	return
	//}

	// Prepare and serve gateway to connection.
	ptyG := NewDirectGateway()

	defer func() { _ = ptyG.Stop(nil, nil) }() //nolint:errcheck

	rpcS := rpc.NewServer()
	if err := rpcS.Register(ptyG); err != nil {
		log.WithError(err).Fatal("handleConn: failed to register pty gateway")
		return
	}
	rpcS.ServeConn(conn)
}

// RequestPty requests a remote pty over dmsg.
func (s *Server) RequestPty(ctx context.Context, rPK cipher.PubKey, conn net.Conn) Gateway {
	log := logging.MustGetLogger("dmsgpty-client:" + rPK.String())
	return NewProxyGateway(NewPtyClient(ctx, log, conn))
}
