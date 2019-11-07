package appserver

import (
	"fmt"
	"net"
	"net/rpc"
	"sync"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/pkg/errors"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
)

// Server is a server for app/visor communication.
type Server struct {
	log      *logging.Logger
	lis      net.Listener
	sockFile string
	rpcS     *rpc.Server
	done     sync.WaitGroup
	stopCh   chan struct{}
}

// NewServer constructs server.
func New(log *logging.Logger, sockFile string, appKey appcommon.Key) (*Server, error) {
	rpcS := rpc.NewServer()
	gateway := NewRPCGateway(logging.MustGetLogger(fmt.Sprintf("rpc_server_%s", appKey)))
	if err := rpcS.RegisterName(string(appKey), gateway); err != nil {
		return nil, errors.Wrap(err, "error registering RPC server for app")
	}

	return &Server{
		log:      log,
		sockFile: sockFile,
		rpcS:     rpcS,
		stopCh:   make(chan struct{}),
	}, nil
}

// ListenAndServe starts listening for incoming app connections via unix socket.
func (s *Server) ListenAndServe() error {
	l, err := net.Listen("unix", s.sockFile)
	if err != nil {
		return err
	}

	s.lis = l

	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}

		s.done.Add(1)
		go s.serveConn(conn)
	}
}

// Close closes the server.
func (s *Server) Close() error {
	err := s.lis.Close()
	close(s.stopCh)
	s.done.Wait()
	return err
}

// serveConn serves RPC on a single connection.
func (s *Server) serveConn(conn net.Conn) {
	go s.rpcS.ServeConn(conn)
	<-s.stopCh
	if err := conn.Close(); err != nil {
		s.log.WithError(err).Error("error closing conn")
	}
	s.done.Done()
}
