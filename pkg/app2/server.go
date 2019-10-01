package app2

import (
	"fmt"
	"io"
	"net"
	"net/rpc"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
)

// Server is a server for app/visor communication.
type Server struct {
	log      *logging.Logger
	sockFile string
	rpcS     *rpc.Server
}

// NewServer constructs server.
func NewServer(log *logging.Logger, sockFile string) *Server {
	return &Server{
		log:      log,
		sockFile: sockFile,
		rpcS:     rpc.NewServer(),
	}
}

// ListenAndServe starts listening for incoming app connections via unix socket.
func (s *Server) ListenAndServe() error {
	l, err := net.Listen("unix", s.sockFile)
	if err != nil {
		return err
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}

		go s.serveConn(conn)
	}
}

// serveConn instantiates RPC gateway for an application.
func (s *Server) serveConn(conn net.Conn) {
	var appKey cipher.PubKey
	if _, err := io.ReadFull(conn, appKey[:]); err != nil {
		s.closeConn(conn)
		s.log.WithError(err).Error("error reading app key")
		return
	}

	appKeyHex := appKey.Hex()

	gateway := newRPCGateway(logging.MustGetLogger(fmt.Sprintf("rpc_gateway_%s", appKeyHex)))
	if err := s.rpcS.RegisterName(appKeyHex, gateway); err != nil {
		s.closeConn(conn)
		s.log.WithError(err).Errorf("error registering rpc gateway for app with key %s", appKeyHex)
		return
	}

	go s.rpcS.ServeConn(conn)
}

// closeConn closes connection and logs error if any.
func (s *Server) closeConn(conn net.Conn) {
	if err := conn.Close(); err != nil {
		s.log.WithError(err).Error("error closing conn")
	}
}
