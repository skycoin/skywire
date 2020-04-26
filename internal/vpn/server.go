package vpn

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/songgao/water"
)

// Server is a VPN server.
type Server struct {
	lisMx     sync.Mutex
	lis       net.Listener
	log       *logging.MasterLogger
	serveOnce sync.Once
	ipGen     *IPGenerator
}

// NewServer creates VPN server instance.
func NewServer(l *logging.MasterLogger) *Server {
	return &Server{
		log:   l,
		ipGen: NewIPGenerator(),
	}
}

// Serve accepts connections from `l` and serves them.
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

// Close shuts server down.
func (s *Server) Close() error {
	s.lisMx.Lock()
	defer s.lisMx.Unlock()

	if s.lis == nil {
		return nil
	}

	err := s.lis.Close()
	s.lis = nil

	return err
}

func (s *Server) closeConn(conn net.Conn) {
	if err := conn.Close(); err != nil {
		s.log.WithError(err).Errorf("Error closing client %s connection", conn.RemoteAddr())
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer s.closeConn(conn)

	tunIP, tunGateway, err := s.shakeHands(conn)
	if err != nil {
		s.log.WithError(err).Errorf("Error negotiating with client %s", conn.RemoteAddr())
		return
	}

	tun, err := water.New(water.Config{
		DeviceType: water.TUN,
	})
	if err != nil {
		s.log.WithError(err).Errorln("Error allocating TUN interface")
		return
	}
	defer func() {
		tunName := tun.Name()
		if err := tun.Close(); err != nil {
			s.log.WithError(err).Errorf("Error closing TUN %s", tunName)
		}
	}()

	s.log.Infof("Allocated TUN %s", tun.Name())

	if err := SetupTUN(tun.Name(), tunIP.String(), TUNNetmask, tunGateway.String(), TUNMTU); err != nil {
		s.log.WithError(err).Errorf("Error setting up TUN %s", tun.Name())
		return
	}

	connToTunDoneCh := make(chan struct{})
	tunToConnCh := make(chan struct{})
	go func() {
		defer close(connToTunDoneCh)

		if _, err := io.Copy(tun, conn); err != nil {
			s.log.WithError(err).Errorf("Error resending traffic from VPN client to TUN %s", tun.Name())
		}
	}()
	go func() {
		defer close(tunToConnCh)

		if _, err := io.Copy(conn, tun); err != nil {
			s.log.WithError(err).Errorf("Error resending traffic from TUN %s to VPN client", tun.Name())
		}
	}()

	// only one side may fail here, so we wait till at least one fails
	select {
	case <-connToTunDoneCh:
	case <-tunToConnCh:
	}
}

func (s *Server) shakeHands(conn net.Conn) (tunIP, tunGateway net.IP, err error) {
	var cHello ClientHello
	if err := ReadJSON(conn, &cHello); err != nil {
		return nil, nil, fmt.Errorf("error reading client hello: %w", err)
	}

	var sHello ServerHello

	for _, ip := range cHello.UnavailablePrivateIPs {
		if err := s.ipGen.Reserve(ip); err != nil {
			// this happens only on malformed IP
			sHello.Status = HandshakeStatusBadRequest
			if err := WriteJSON(conn, &sHello); err != nil {
				s.log.WithError(err).Errorln("Error sending server hello")
			}

			return nil, nil, fmt.Errorf("error reserving IP %s: %w", ip.String(), err)
		}
	}

	subnet, err := s.ipGen.Next()
	if err != nil {
		sHello.Status = HandshakeNoFreeIPs
		if err := WriteJSON(conn, &sHello); err != nil {
			s.log.WithError(err).Errorln("Error sending server hello")
		}

		return nil, nil, fmt.Errorf("error getting free subnet IP: %w", err)
	}

	subnetOctets, err := fetchIPv4Octets(subnet)
	if err != nil {
		sHello.Status = HandshakeStatusInternalError
		if err := WriteJSON(conn, &sHello); err != nil {
			s.log.WithError(err).Errorln("Error sending server hello")
		}

		return nil, nil, fmt.Errorf("error breaking IP into octets: %w", err)
	}

	// basically IP address comprised of `subnetOctets` items is the IP address of the subnet,
	// we're going to work with. In this subnet we're giving 4 IP addresses: IP and gateway for
	// the server-side TUN and IP and gateway for the client-side TUN. We do this as follows:
	// - Server-side TUN gateway = subnet IP + 1
	// - Server-side TUN IP = subnet IP + 2
	// - Client-side TUN gateway = subnet IP + 3
	// - Client-site TUN IP = subnet IP + 4

	sTUNIP := net.IPv4(subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3]+2)
	sTUNGateway := net.IPv4(subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3]+1)

	cTUNIP := net.IPv4(subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3]+4)
	cTUNGateway := net.IPv4(subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3]+3)

	sHello.TUNIP = cTUNIP
	sHello.TUNGateway = cTUNGateway

	if err := WriteJSON(conn, &sHello); err != nil {
		return nil, nil, fmt.Errorf("error finishing hadnshake: error sending server hello: %w", err)
	}

	return sTUNIP, sTUNGateway, nil
}
