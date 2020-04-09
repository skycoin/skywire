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
	tunIP      = "192.168.255.1"
	tunNetmask = "255.255.0.0"
	tunGateway = "192.168.255.0"
	tunMTU     = 1500
)

type Server struct {
	log       *logging.MasterLogger
	serveOnce sync.Once
}

func NewServer(l *logging.MasterLogger) *Server {
	return &Server{
		log: l,
	}
}

func (s *Server) Serve(l net.Listener) error {
	serveErr := errors.New("already serving")
	s.serveOnce.Do(func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				serveErr = fmt.Errorf("failed to accept client connection: %w", err)
			}

			go s.serveConn(conn)
		}
	})

	return serveErr
}

func (s *Server) serveConn(conn net.Conn) {
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

	// TODO: generate IPs, each client should have a separate TUN with separate IP and gateway
	SetupTUN(ifc.Name(), tunIP, tunNetmask, tunGateway, tunMTU)

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
