// Package vpn pkg/vpn/server.go c3-app-vpn
package vpn

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
)

// Server is a VPN server.
type Server struct {
	cfg                        ServerConfig
	log                        logging.FieldLogger
	lisMx                      sync.Mutex
	lis                        net.Listener
	serveOnce                  sync.Once
	ipGen                      *IPGenerator
	defaultNetworkInterface    string
	defaultNetworkInterfaceIPs []net.IP
	ipv4ForwardingVal          string
	ipv6ForwardingVal          string
	iptablesForwardPolicy      string
	appCl                      *app.Client
	whitelist                  map[cipher.PubKey]struct{}
	useWL                      bool
}

// NewServer creates VPN server instance.
func NewServer(cfg ServerConfig, appCl *app.Client) (*Server, error) {
	var defaultNetworkIfc string

	wlMap := make(map[cipher.PubKey]struct{})
	for _, pk := range cfg.Whitelist {
		wlMap[pk] = struct{}{}
	}

	s := &Server{
		cfg:       cfg,
		log:       cfg.Log,
		ipGen:     NewIPGenerator(),
		appCl:     appCl,
		whitelist: wlMap,
		useWL:     len(cfg.Whitelist) > 0,
	}
	if s.log == nil {
		s.log = logging.MustGetLogger("vpn_server")
	}

	if cfg.NetworkInterface != "" {
		// An explicitly configured interface (--netifc) always wins over
		// auto-detection. The operator knows which interface should carry egress,
		// and auto-detect can pick the wrong one when several default routes exist
		// (e.g. a VPN/overlay like Tailscale owning the default route).
		defaultNetworkIfc = cfg.NetworkInterface
	} else {
		defaultNetworkIfcs, err := netutil.DefaultNetworkInterface()
		if err != nil {
			return nil, fmt.Errorf("error getting default network interface: %w", err)
		}
		ifcs := strings.Split(defaultNetworkIfcs, "\n")
		defaultNetworkIfc = ifcs[0]
		if len(ifcs) > 1 {
			s.log.Warnf("Several default routes %v, using %s (lowest metric); set another via --netifc", ifcs, defaultNetworkIfc)
		}
	}

	s.log.Infof("Got default network interface: %s", defaultNetworkIfc)

	// The interface IPs are informational only (stored, not used for routing/NAT),
	// so a lookup failure must not stop the server — on Windows the interface may
	// be named in a form NetworkInterfaceIPs can't resolve (or be a placeholder
	// like the loopback used for tests), yet the server can still serve fine.
	defaultNetworkIfcIPs, err := netutil.NetworkInterfaceIPs(defaultNetworkIfc)
	if err != nil {
		s.log.WithError(err).Warnf("Could not get IPs of interface %s (non-fatal)", defaultNetworkIfc)
	}

	s.log.Infof("Got IPs of interface %s: %v", defaultNetworkIfc, defaultNetworkIfcIPs)

	ipv4ForwardingVal, err := GetIPv4ForwardingValue()
	if err != nil {
		return nil, fmt.Errorf("error getting IPv4 forwarding value: %w", err)
	}
	ipv6ForwardingVal, err := GetIPv6ForwardingValue()
	if err != nil {
		return nil, fmt.Errorf("error getting IPv6 forwarding value")
	}

	s.log.Infof("Old IP forwarding values: IPv4: %s, IPv6: %s", ipv4ForwardingVal, ipv6ForwardingVal)

	iptablesForwardPolicy, err := GetIPTablesForwardPolicy()
	if err != nil {
		return nil, fmt.Errorf("error getting iptables forward policy: %w", err)
	}

	s.log.Infof("Old iptables forward policy: %s", iptablesForwardPolicy)

	s.defaultNetworkInterface = defaultNetworkIfc
	s.defaultNetworkInterfaceIPs = defaultNetworkIfcIPs
	s.ipv4ForwardingVal = ipv4ForwardingVal
	s.ipv6ForwardingVal = ipv6ForwardingVal
	s.iptablesForwardPolicy = iptablesForwardPolicy

	return s, nil
}

// Serve accepts connections from `l` and serves them.
func (s *Server) Serve(l net.Listener) error {
	serveErr := errors.New("already serving")
	s.serveOnce.Do(func() {
		s.setAppStatus(appserver.AppDetailedStatusStarting)
		if err := EnableIPv4Forwarding(s.log); err != nil {
			serveErr = fmt.Errorf("error enabling IPv4 forwarding: %w", err)
			return
		}
		s.log.Info("Set IPv4 forwarding = 1")
		defer func() {
			s.revertIPv4ForwardingValue()
		}()

		if err := EnableIPv6Forwarding(); err != nil {
			serveErr = fmt.Errorf("error enabling IPv6 forwarding: %w", err)
			return
		}
		s.log.Info("Set IPv6 forwarding = 1")
		defer func() {
			s.revertIPv6ForwardingValue()
		}()

		if err := EnableIPMasquerading(s.log, s.defaultNetworkInterface); err != nil {
			serveErr = fmt.Errorf("error enabling IP masquerading for %s: %w", s.defaultNetworkInterface, err)
			return
		}

		s.log.Info("Enabled IP masquerading")

		defer func() {
			s.disableIPMasquerading()
		}()

		if err := SetIPTablesForwardAcceptPolicy(); err != nil {
			serveErr = fmt.Errorf("error settings iptables forward policy to ACCEPT")
			return
		}
		s.log.Info("Set iptables forward policy to ACCEPT")

		defer func() {
			s.restoreIPTablesForwardPolicy()
		}()

		s.lisMx.Lock()
		s.lis = l
		s.lisMx.Unlock()
		s.setAppStatus(appserver.AppDetailedStatusRunning)
		// Accept off the local listener parameter, not s.lis.
		// Close() sets `s.lis = nil` after closing it, and the
		// previous code dereferenced `s.lis.Accept()` without
		// holding s.lisMx — so any non-net.ErrClosed accept error
		// (e.g. a dmsg listener returning "io: read/write on
		// closed pipe" mid-shutdown) routed to `continue`, and the
		// next iteration nil-deref'd s.lis. The local `l` always
		// points at the listener that was passed in; closing it
		// elsewhere is fine, calling .Accept() on a closed
		// listener returns an error rather than panicking.
		for {
			conn, err := l.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				// Best-effort match for transport-level shutdowns
				// that a wrapped listener may surface as a
				// non-net.ErrClosed error (e.g. dmsg's
				// "use of closed network connection" /
				// "io: read/write on closed pipe"). Treat any
				// accept error after Close() as terminal.
				s.lisMx.Lock()
				closed := s.lis == nil
				s.lisMx.Unlock()
				if closed {
					return
				}
				s.log.WithError(err).Warn("Failed to accept VPN client connection, continuing")
				continue
			}

			go s.serveConn(conn)
		}
	})

	s.setAppError(serveErr)
	return serveErr
}

// Close shuts server down.
func (s *Server) Close() error {
	s.lisMx.Lock()
	defer s.lisMx.Unlock()

	s.revertIPv4ForwardingValue()
	s.revertIPv6ForwardingValue()
	s.disableIPMasquerading()
	s.restoreIPTablesForwardPolicy()

	if s.lis == nil {
		return nil
	}

	err := s.lis.Close()
	s.lis = nil

	return err
}

func (s *Server) revertIPv4ForwardingValue() {
	if err := SetIPv4ForwardingValue(s.log, s.ipv4ForwardingVal); err != nil {
		s.log.WithError(err).Error("Error reverting IPv4 forwarding")
	} else {
		s.log.Infof("Set IPv4 forwarding = %s", s.ipv4ForwardingVal)
	}
}

func (s *Server) revertIPv6ForwardingValue() {
	if err := SetIPv6ForwardingValue(s.ipv6ForwardingVal); err != nil {
		s.log.WithError(err).Error("Error reverting IPv6 forwarding")
	} else {
		s.log.Infof("Set IPv6 forwarding = %s", s.ipv6ForwardingVal)
	}
}

func (s *Server) disableIPMasquerading() {
	if err := DisableIPMasquerading(s.log, s.defaultNetworkInterface); err != nil {
		s.log.WithError(err).Errorf("Error disabling IP masquerading for %s", s.defaultNetworkInterface)
	} else {
		s.log.Infof("Disabled IP masquerading for %s", s.defaultNetworkInterface)
	}
}

func (s *Server) restoreIPTablesForwardPolicy() {
	if err := SetIPTablesForwardPolicy(s.iptablesForwardPolicy); err != nil {
		s.log.WithError(err).Errorf("Error restoring iptables forward policy to %s", s.iptablesForwardPolicy)
	} else {
		s.log.Infof("Restored iptables forward policy to %s", s.iptablesForwardPolicy)
	}
}

func (s *Server) closeConn(conn net.Conn) {
	if err := conn.Close(); err != nil {
		s.log.WithError(err).Warnf("Error closing client %s connection", conn.RemoteAddr())
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer s.closeConn(conn)

	// Check whitelist if enabled
	if s.useWL {
		remotePK, err := s.getRemotePK(conn)
		if err != nil {
			s.log.WithError(err).Warn("Failed to get remote PK, rejecting connection")
			return
		}

		if _, allowed := s.whitelist[remotePK]; !allowed {
			s.log.Warnf("Connection rejected: %s not in whitelist", remotePK.Hex())
			return
		}

		s.log.Infof("Whitelisted connection accepted from %s", remotePK.Hex())
	}

	tunIP, tunGateway, allowTrafficToLocalNet, err := s.shakeHands(conn)
	if err != nil {
		s.log.WithError(err).Errorf("Error negotiating with client %s", conn.RemoteAddr())
		return
	}
	defer allowTrafficToLocalNet()

	tun, err := newTUNDevice()
	if err != nil {
		s.log.WithError(err).Error("Error allocating TUN interface")
		return
	}
	defer func() {
		if err := tun.Close(); err != nil {
			s.log.WithError(err).Warnf("Error closing TUN %s", tun.Name())
		}
	}()

	s.log.Infof("Allocated TUN %s", tun.Name())

	if err := s.SetupTUN(tun.Name(), tunIP.String()+TUNNetmaskCIDR, tunGateway.String(), TUNMTU); err != nil {
		s.log.WithError(err).Errorf("Error setting up TUN %s", tun.Name())
		return
	}

	connToTunDoneCh := make(chan struct{})
	tunToConnCh := make(chan struct{})
	go func() {
		defer close(connToTunDoneCh)

		if _, err := io.Copy(tun, conn); err != nil {
			// when the vpn-client is closed we get the error "EOF"
			if err.Error() != io.EOF.Error() {
				s.log.WithError(err).Errorf("Error resending traffic from VPN client to TUN %s", tun.Name())
			}
		}
	}()
	go func() {
		defer close(tunToConnCh)

		if _, err := io.Copy(conn, tun); err != nil {
			// when the vpn-client is closed we get the error "read tun: file already closed"
			if err.Error() != "read tun: file already closed" {
				s.log.WithError(err).Errorf("Error resending traffic from TUN %s to VPN client", tun.Name())
			}
		}
	}()

	// only one side may fail here, so we wait till at least one fails
	select {
	case <-connToTunDoneCh:
	case <-tunToConnCh:
	}
}

func (s *Server) shakeHands(conn net.Conn) (tunIP, tunGateway netip.Addr, unsecureVPN func(), err error) {
	var cHello ClientHello
	if err := ReadJSON(conn, &cHello); err != nil {
		return netip.Addr{}, netip.Addr{}, nil, fmt.Errorf("error reading client hello: %w", err)
	}

	// default value
	unsecureVPN = func() {}

	s.log.Debugf("Got client hello: %v", cHello)

	for _, ip := range cHello.UnavailablePrivateIPs {
		if err := s.ipGen.Reserve(ip); err != nil {
			// this happens only on malformed IP
			s.sendServerErrHello(conn, HandshakeStatusBadRequest)
			return netip.Addr{}, netip.Addr{}, nil, fmt.Errorf("error reserving IP %s: %w", ip.String(), err)
		}
	}

	subnet, err := s.ipGen.Next()
	if err != nil {
		s.sendServerErrHello(conn, HandshakeNoFreeIPs)
		return netip.Addr{}, netip.Addr{}, nil, fmt.Errorf("error getting free subnet IP: %w", err)
	}

	subnetOctets, err := fetchIPv4Octets(subnet)
	if err != nil {
		s.sendServerErrHello(conn, HandshakeStatusInternalError)
		return netip.Addr{}, netip.Addr{}, nil, fmt.Errorf("error breaking IP into octets: %w", err)
	}

	// basically IP address comprised of `subnetOctets` items is the IP address of the subnet,
	// we're going to work with. In this subnet we're giving 4 IP addresses: IP and gateway for
	// the server-side TUN and IP and gateway for the client-side TUN. We do this as follows:
	// - Server-side TUN gateway = subnet IP + 1
	// - Server-side TUN IP = subnet IP + 2
	// - Client-side TUN gateway = subnet IP + 3
	// - Client-site TUN IP = subnet IP + 4

	sTUNIP := netip.AddrFrom4([4]byte{subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3] + 2})
	sTUNGateway := netip.AddrFrom4([4]byte{subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3] + 1})

	cTUNIP := netip.AddrFrom4([4]byte{subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3] + 4})
	cTUNGateway := netip.AddrFrom4([4]byte{subnetOctets[0], subnetOctets[1], subnetOctets[2], subnetOctets[3] + 3})

	if s.cfg.Secure {
		if err := BlockIPToLocalNetwork(cTUNIP, sTUNIP); err != nil { //nolint
			s.sendServerErrHello(conn, HandshakeStatusInternalError)
			return netip.Addr{}, netip.Addr{}, nil,
				fmt.Errorf("error securing local network for IP %s: %w", cTUNIP, err)
		}

		unsecureVPN = func() {
			if err := AllowIPToLocalNetwork(cTUNIP, sTUNIP); err != nil {
				s.log.WithError(err).Error("Error allowing traffic to local network")
			}
		}
	}

	sHello := ServerHello{
		Status:     HandshakeStatusOK,
		TUNIP:      cTUNIP,
		TUNGateway: cTUNGateway,
	}

	if err := WriteJSON(conn, &sHello); err != nil {
		unsecureVPN()
		return netip.Addr{}, netip.Addr{}, nil, fmt.Errorf("error finishing handshake: error sending server hello: %w", err)
	}

	return sTUNIP, sTUNGateway, unsecureVPN, nil
}

func (s *Server) setAppStatus(status appserver.AppDetailedStatus) {
	if err := s.appCl.SetDetailedStatus(string(status)); err != nil {
		s.log.WithError(err).Warnf("Failed to set status %v", status)
	}
}

func (s *Server) setAppError(appErr error) {
	if err := s.appCl.SetError(appErr.Error()); err != nil {
		s.log.WithError(err).Warnf("Failed to set error %v", appErr)
	}
}

func (s *Server) sendServerErrHello(conn net.Conn, status HandshakeStatus) {
	sHello := ServerHello{
		Status: status,
	}

	if err := WriteJSON(conn, &sHello); err != nil {
		s.log.WithError(err).Warn("Error sending server hello")
	}
}

// getRemotePK extracts the remote public key from the connection
func (s *Server) getRemotePK(conn net.Conn) (cipher.PubKey, error) {
	// Try direct type assertion first (app framework connections already use appnet.Addr)
	if rAddr, ok := conn.RemoteAddr().(appnet.Addr); ok {
		return rAddr.PubKey, nil
	}

	// Fall back to ConvertAddr for other connection types (dmsg, routing)
	addr, err := appnet.ConvertAddr(conn.RemoteAddr())
	if err != nil {
		return cipher.PubKey{}, fmt.Errorf("failed to get remote address: %w", err)
	}

	return addr.PubKey, nil
}
