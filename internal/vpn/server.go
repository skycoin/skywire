package vpn

import (
	"errors"
	"fmt"
	"io"
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

	tunIP, _, err := s.ipGen.Next()
	if err != nil {
		s.log.WithError(err).Errorf("failed to get free IP for TUN for client %s", conn.RemoteAddr())
		return
	}

	tun, err := water.New(water.Config{
		DeviceType: water.TUN,
	})
	if nil != err {
		s.log.WithError(err).Errorln("Error allocating TUN interface")
	}
	defer func() {
		tunName := tun.Name()
		if err := tun.Close(); err != nil {
			s.log.WithError(err).Errorf("Error closing TUN %s", tunName)
		}
	}()

	s.log.Infof("Allocated TUN %s", tun.Name())

	if err := SetupTUN(tun.Name(), tunIP.String(), tunNetmask, "192.168.255.1", tunMTU); err != nil {
		s.log.WithError(err).Errorf("Error setting up TUN %s", tun.Name())
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()

		if _, err := io.Copy(tun, conn); err != nil {
			s.log.WithError(err).Errorf("Error resending traffic from TUN %s to client", tun.Name())
		}
	}()
	go func() {
		defer wg.Done()

		if _, err := io.Copy(conn, tun); err != nil {
			s.log.WithError(err).Errorf("Error resending traffic from VPN client to TUN %s", tun.Name())
		}
	}()

	wg.Wait()
}
