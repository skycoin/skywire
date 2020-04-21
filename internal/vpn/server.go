package vpn

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/songgao/water"

	"github.com/SkycoinProject/skycoin/src/util/logging"
)

const (
	tunNetmask = "255.255.255.248"
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

	tunIP, tunGateway, err := s.negotiate(conn)
	if err != nil {
		s.log.WithError(err).Errorf("Error negotiating with client %s", conn.RemoteAddr())
		return
	}

	tun, err := water.New(water.Config{
		DeviceType: water.TUN,
	})
	if nil != err {
		s.log.WithError(err).Errorln("Error allocating TUN interface")
	}
	defer func() {
		s.log.Errorln("DONE SERVING, CLOSING TUN")
		tunName := tun.Name()
		if err := tun.Close(); err != nil {
			s.log.WithError(err).Errorf("Error closing TUN %s", tunName)
		}
	}()

	s.log.Infof("Allocated TUN %s", tun.Name())

	if err := SetupTUN(tun.Name(), tunIP.String(), tunNetmask, tunGateway.String(), tunMTU); err != nil {
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
		s.log.Errorln("DONE COPYING FROM CONN TO TUN")
	}()
	go func() {
		defer close(tunToConnCh)

		if _, err := io.Copy(conn, tun); err != nil {
			s.log.WithError(err).Errorf("Error resending traffic from TUN %s to VPN client", tun.Name())
		}
		s.log.Errorln("DONE COPYING FROM TUN TO CONN")
	}()

	select {
	case <-connToTunDoneCh:
	case <-tunToConnCh:
	}
}

func (s *Server) negotiate(conn net.Conn) (tunIP, tunGateway net.IP, err error) {
	var cHelloBytes []byte
	buf := make([]byte, 1024)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, nil, fmt.Errorf("error reading client hello: %w", err)
		}

		cHelloBytes = append(cHelloBytes, buf[:n]...)

		if n < 1024 {
			break
		}
	}

	var cHello ClientHello
	if err := json.Unmarshal(cHelloBytes, &cHello); err != nil {
		return nil, nil, fmt.Errorf("error unmarshaling client helloL %w", err)
	}

	var sHello ServerHello

	for _, ip := range cHello.UnavailablePrivateIPs {
		if err := s.ipGen.Reserve(ip); err != nil {
			sHello.Status = NegotiationStatusIPNotReserved
			if err := s.sendServerHello(conn, sHello); err != nil {
				s.log.WithError(err).Errorln("Error sending server hello")
			}

			return nil, nil, fmt.Errorf("error reserving IP %s: %w", ip.String(), err)
		}
	}

	subnet, err := s.ipGen.Next()
	if err != nil {
		sHello.Status = NegotiationStatusInternalError
		if err := s.sendServerHello(conn, sHello); err != nil {
			s.log.WithError(err).Errorln("Error sending server hello")
		}

		return nil, nil, fmt.Errorf("error getting free subnet IP: %w", err)
	}

	subnetOctets, err := fetchIPv4Bytes(subnet)
	if err != nil {
		sHello.Status = NegotiationStatusInternalError
		if err := s.sendServerHello(conn, sHello); err != nil {
			s.log.WithError(err).Errorln("Error sending server hello")
		}

		return nil, nil, fmt.Errorf("error breaking IP into octets: %w", err)
	}

	sTUNIP := net.IPv4(subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3]+2)
	sTUNGateway := net.IPv4(subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3]+1)

	cTUNIP := net.IPv4(subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3]+4)
	cTUNGateway := net.IPv4(subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3]+3)

	sHello.TUNIP = cTUNIP
	sHello.TUNGateway = cTUNGateway

	if err := s.sendServerHello(conn, sHello); err != nil {
		return nil, nil, fmt.Errorf("error finishing negotiation: error sending server hello: %w", err)
	}

	return sTUNIP, sTUNGateway, nil
}

func (s *Server) sendServerHello(conn net.Conn, h ServerHello) error {
	sHelloBytes, err := json.Marshal(&h)
	if err != nil {
		return fmt.Errorf("error marshaling server hello: %w", err)
	}

	n, err := conn.Write(sHelloBytes)
	if err != nil {
		return fmt.Errorf("error writing server hello: %w", err)
	}

	totalSent := n
	for totalSent != len(sHelloBytes) {
		n, err := conn.Write(sHelloBytes[totalSent:])
		if err != nil {
			return fmt.Errorf("error writing server hello: %w", err)
		}

		totalSent += n
	}

	return nil
}
