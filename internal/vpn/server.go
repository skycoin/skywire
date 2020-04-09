package vpn

import (
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/songgao/water"

	"github.com/SkycoinProject/skycoin/src/util/logging"
)

const (
	tunNetmask = "255.255.0.0"
	tunMTU     = 1500
)

type Server struct {
	lisMx     sync.Mutex
	lis       net.Listener
	log       *logging.MasterLogger
	serveOnce sync.Once
	ipGen     *TUNIPGenerator
}

func NewServer(l *logging.MasterLogger) *Server {
	return &Server{
		log:   l,
		ipGen: NewTUNIPGenerator(),
	}
}

func (s *Server) Serve(l net.Listener) error {
	serveErr := errors.New("already serving")
	s.serveOnce.Do(func() {
		s.lisMx.Lock()
		s.lis = l
		s.lisMx.Unlock()

		for {
			conn, err := s.lis.Accept()
			if err != nil {
				serveErr = fmt.Errorf("failed to accept client connection: %w", err)
				return
			}

			go s.serveConn(conn)
		}
	})

	return serveErr
}

func (s *Server) Close() error {
	s.lisMx.Lock()
	defer s.lisMx.Unlock()

	if s.lis == nil {
		return nil
	}

	return s.lis.Close()
}

func (s *Server) closeConn(conn net.Conn) {
	if err := conn.Close(); err != nil {
		s.log.WithError(err).Errorf("Error closing client %s connection", conn.RemoteAddr())
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer s.closeConn(conn)

	tunIP, tunGateway, err := s.ipGen.Next()
	if err != nil {
		s.log.WithError(err).Errorf("failed to get free IP for TUN for client %s", conn.RemoteAddr())
		return
	}

	ifc, err := water.New(water.Config{
		DeviceType: water.TUN,
	})
	if nil != err {
		s.log.WithError(err).Errorln("Error allocating TUN interface")
	}
	defer func() {
		tunName := ifc.Name()
		if err := ifc.Close(); err != nil {
			s.log.WithError(err).Errorf("Error closing TUN %s", tunName)
		}
	}()

	s.log.Infof("Allocated TUN %s", ifc.Name())

	SetupTUN(ifc.Name(), tunIP.String(), tunNetmask, tunGateway.String(), tunMTU)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()

		if err := CopyTraffic(ifc, conn); err != nil {
			s.log.WithError(err).Errorf("Error resending traffic from TUN %s to client", ifc.Name())
		}
	}()
	go func() {
		defer wg.Done()

		if err := CopyTraffic(conn, ifc); err != nil {
			s.log.WithError(err).Error("Error resending traffic from VPN client to TUN %s", ifc.Name())
		}
	}()

	wg.Wait()
}
