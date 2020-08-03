package appserver

import (
	"fmt"
	"net"
	"net/rpc"
	"strings"
	"sync"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/appcommon"
)

// Server is a server for app/visor communication.
type Server struct {
	log    *logging.Logger
	lis    net.Listener
	addr   string
	rpcS   *rpc.Server
	done   sync.WaitGroup
	stopCh chan struct{}
}

// New constructs server.
func New(log *logging.Logger, addr string) *Server {
	return &Server{
		log:    log,
		addr:   addr,
		rpcS:   rpc.NewServer(),
		stopCh: make(chan struct{}),
	}
}

// Register registers an app key in RPC server.
func (s *Server) Register(appKey appcommon.Key) error {
	logger := logging.MustGetLogger(fmt.Sprintf("app_gateway:%s", appKey))
	gateway := NewRPCGateway(logger)

	return s.rpcS.RegisterName(string(appKey), gateway)
}

// ListenAndServe starts listening for incoming app connections via tcp socket.
func (s *Server) ListenAndServe() error {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	s.lis = l

	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}

		s.done.Add(1) // nolint: gomnd

		go s.serveConn(conn)
	}
}

// Close closes the server.
func (s *Server) Close() error {
	var err error

	if s.lis != nil {
		err = s.lis.Close()
	}

	close(s.stopCh)

	s.done.Wait()

	return err
}

// serveConn serves RPC on a single connection.
func (s *Server) serveConn(conn net.Conn) {
	go s.rpcS.ServeConn(conn)

	<-s.stopCh

	if err := conn.Close(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
		s.log.WithError(err).Error("Unexpected error while closing conn.")
	}

	s.done.Done()
}
