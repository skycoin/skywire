package app2

import (
	"fmt"
	"net"
	"net/rpc"

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

	s.rpcS.Accept(l)

	return nil
}

// AllowApp allows app with the key `appKey` to do RPC calls.
func (s *Server) AllowApp(appKey string) error {
	gateway := newRPCGateway(logging.MustGetLogger(fmt.Sprintf("rpc_gateway_%s", appKey)))
	return s.rpcS.RegisterName(appKey, gateway)
}
