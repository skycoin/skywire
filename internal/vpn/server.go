// Package vpn internal/vpn/server.go
package vpn

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
)

// Server is a VPN server.
type Server struct {
	cfg                        ServerConfig
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
}

// NewServer creates VPN server instance.
func NewServer(cfg ServerConfig, appCl *app.Client) (*Server, error) {
	var defaultNetworkIfc string
	s := &Server{
		cfg:   cfg,
		ipGen: NewIPGenerator(),
		appCl: appCl,
	}

	defaultNetworkIfcs, err := netutil.DefaultNetworkInterface()
	if err != nil {
		return nil, fmt.Errorf("error getting default network interface: %w", err)
	}
	ifcs, hasMultiple := s.hasMultipleNetworkInterfaces(defaultNetworkIfcs)
	if hasMultiple {
		if cfg.NetworkInterface == "" {
			return nil, fmt.Errorf("multiple default network interfaces detected...set a default one for VPN server or remove one: %v", ifcs)
		} else if !s.validateInterface(ifcs, cfg.NetworkInterface) {
			return nil, fmt.Errorf("network interface value in config is not in default network interfaces detected: %v", ifcs)
		}
		defaultNetworkIfc = cfg.NetworkInterface
	} else {
		defaultNetworkIfc = defaultNetworkIfcs
	}

	fmt.Printf("Got default network interface: %s\n", defaultNetworkIfc)

	defaultNetworkIfcIPs, err := netutil.NetworkInterfaceIPs(defaultNetworkIfc)
	if err != nil {
		return nil, fmt.Errorf("error getting IPs of interface %s: %w", defaultNetworkIfc, err)
	}

	fmt.Printf("Got IPs of interface %s: %v\n", defaultNetworkIfc, defaultNetworkIfcIPs)

	ipv4ForwardingVal, err := GetIPv4ForwardingValue()
	if err != nil {
		return nil, fmt.Errorf("error getting IPv4 forwarding value: %w", err)
	}
	ipv6ForwardingVal, err := GetIPv6ForwardingValue()
	if err != nil {
		return nil, fmt.Errorf("error getting IPv6 forwarding value")
	}

	fmt.Println("Old IP forwarding values:")
	fmt.Printf("IPv4: %s, IPv6: %s\n", ipv4ForwardingVal, ipv6ForwardingVal)

	iptablesForwardPolicy, err := GetIPTablesForwardPolicy()
	if err != nil {
		return nil, fmt.Errorf("error getting iptables forward policy: %w", err)
	}

	fmt.Printf("Old iptables forward policy: %s\n", iptablesForwardPolicy)

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
		if err := EnableIPv4Forwarding(); err != nil {
			serveErr = fmt.Errorf("error enabling IPv4 forwarding: %w", err)
			return
		}
		fmt.Println("Set IPv4 forwarding = 1")
		defer func() {
			s.revertIPv4ForwardingValue()
		}()

		if err := EnableIPv6Forwarding(); err != nil {
			serveErr = fmt.Errorf("error enabling IPv6 forwarding: %w", err)
			return
		}
		fmt.Println("Set IPv6 forwarding = 1")
		defer func() {
			s.revertIPv6ForwardingValue()
		}()

		if err := EnableIPMasquerading(s.defaultNetworkInterface); err != nil {
			serveErr = fmt.Errorf("error enabling IP masquerading for %s: %w", s.defaultNetworkInterface, err)
			return
		}

		fmt.Println("Enabled IP masquerading")

		defer func() {
			s.disableIPMasquerading()
		}()

		if err := SetIPTablesForwardAcceptPolicy(); err != nil {
			serveErr = fmt.Errorf("error settings iptables forward policy to ACCEPT")
			return
		}
		fmt.Println("Set iptables forward policy to ACCEPT")

		defer func() {
			s.restoreIPTablesForwardPolicy()
		}()

		s.lisMx.Lock()
		s.lis = l
		s.lisMx.Unlock()
		s.setAppStatus(appserver.AppDetailedStatusRunning)
		for {
			conn, err := s.lis.Accept()
			if err != nil {
				serveErr = fmt.Errorf("failed to accept client connection: %w", err)
				return
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
	if err := SetIPv4ForwardingValue(s.ipv4ForwardingVal); err != nil {
		print(fmt.Sprintf("Error reverting IPv4 forwarding: %v\n", err))
	} else {
		fmt.Printf("Set IPv4 forwarding = %s\n", s.ipv4ForwardingVal)
	}
}

func (s *Server) revertIPv6ForwardingValue() {
	if err := SetIPv6ForwardingValue(s.ipv6ForwardingVal); err != nil {
		print(fmt.Sprintf("Error reverting IPv6 forwarding: %v\n", err))
	} else {
		fmt.Printf("Set IPv6 forwarding = %s\n", s.ipv6ForwardingVal)
	}
}

func (s *Server) disableIPMasquerading() {
	if err := DisableIPMasquerading(s.defaultNetworkInterface); err != nil {
		print(fmt.Sprintf("Error disabling IP masquerading for %s: %v\n", s.defaultNetworkInterface, err))
	} else {
		fmt.Printf("Disabled IP masquerading for %s\n", s.defaultNetworkInterface)
	}
}

func (s *Server) restoreIPTablesForwardPolicy() {
	if err := SetIPTablesForwardPolicy(s.iptablesForwardPolicy); err != nil {
		print(fmt.Sprintf("Error restoring iptables forward policy to %s: %v\n", s.iptablesForwardPolicy, err))
	} else {
		fmt.Printf("Restored iptables forward policy to %s\n", s.iptablesForwardPolicy)
	}
}

func (s *Server) closeConn(conn net.Conn) {
	if err := conn.Close(); err != nil {
		print(fmt.Sprintf("Error closing client %s connection: %v\n", conn.RemoteAddr(), err))
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer s.closeConn(conn)

	tunIP, tunGateway, allowTrafficToLocalNet, err := s.shakeHands(conn)
	if err != nil {
		print(fmt.Sprintf("Error negotiating with client %s: %v\n", conn.RemoteAddr(), err))
		return
	}
	defer allowTrafficToLocalNet()

	tun, err := newTUNDevice()
	if err != nil {
		print(fmt.Sprintf("Error allocating TUN interface: %v\n", err))
		return
	}
	defer func() {
		if err := tun.Close(); err != nil {
			print(fmt.Sprintf("Error closing TUN %s: %v\n", tun.Name(), err))
		}
	}()

	fmt.Printf("Allocated TUN %s", tun.Name())

	if err := s.SetupTUN(tun.Name(), tunIP.String()+TUNNetmaskCIDR, tunGateway.String(), TUNMTU); err != nil {
		print(fmt.Sprintf("Error setting up TUN %s: %v", tun.Name(), err))
		return
	}

	connToTunDoneCh := make(chan struct{})
	tunToConnCh := make(chan struct{})
	go func() {
		defer close(connToTunDoneCh)

		if _, err := io.Copy(tun, conn); err != nil {
			// when the vpn-client is closed we get the error "EOF"
			if err.Error() != io.EOF.Error() {
				print(fmt.Sprintf("Error resending traffic from VPN client to TUN %s: %v\n", tun.Name(), err))
			}
		}
	}()
	go func() {
		defer close(tunToConnCh)

		if _, err := io.Copy(conn, tun); err != nil {
			// when the vpn-client is closed we get the error "read tun: file already closed"
			if err.Error() != "read tun: file already closed" {
				print(fmt.Sprintf("Error resending traffic from TUN %s to VPN client: %v\n", tun.Name(), err))
			}
		}
	}()

	// only one side may fail here, so we wait till at least one fails
	select {
	case <-connToTunDoneCh:
	case <-tunToConnCh:
	}
}

func (s *Server) shakeHands(conn net.Conn) (tunIP, tunGateway net.IP, unsecureVPN func(), err error) {
	var cHello ClientHello
	if err := ReadJSON(conn, &cHello); err != nil {
		return nil, nil, nil, fmt.Errorf("error reading client hello: %w", err)
	}

	// default value
	unsecureVPN = func() {}

	fmt.Printf("Got client hello: %v", cHello)

	if s.cfg.Passcode != "" && cHello.Passcode != s.cfg.Passcode {
		s.sendServerErrHello(conn, HandshakeStatusForbidden)
		return nil, nil, nil, errors.New("got wrong passcode from client")
	}

	for _, ip := range cHello.UnavailablePrivateIPs {
		if err := s.ipGen.Reserve(ip); err != nil {
			// this happens only on malformed IP
			s.sendServerErrHello(conn, HandshakeStatusBadRequest)
			return nil, nil, nil, fmt.Errorf("error reserving IP %s: %w", ip.String(), err)
		}
	}

	subnet, err := s.ipGen.Next()
	if err != nil {
		s.sendServerErrHello(conn, HandshakeNoFreeIPs)
		return nil, nil, nil, fmt.Errorf("error getting free subnet IP: %w", err)
	}

	subnetOctets, err := fetchIPv4Octets(subnet)
	if err != nil {
		s.sendServerErrHello(conn, HandshakeStatusInternalError)
		return nil, nil, nil, fmt.Errorf("error breaking IP into octets: %w", err)
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

	if s.cfg.Secure {
		if err := BlockIPToLocalNetwork(cTUNIP, sTUNIP); err != nil {
			s.sendServerErrHello(conn, HandshakeStatusInternalError)
			return nil, nil, nil,
				fmt.Errorf("error securing local network for IP %s: %w", cTUNIP, err)
		}

		unsecureVPN = func() {
			if err := AllowIPToLocalNetwork(cTUNIP, sTUNIP); err != nil {
				print(fmt.Sprintf("Error allowing traffic to local network: %v\n", err))
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
		return nil, nil, nil, fmt.Errorf("error finishing handshake: error sending server hello: %w", err)
	}

	return sTUNIP, sTUNGateway, unsecureVPN, nil
}

func (s *Server) setAppStatus(status appserver.AppDetailedStatus) {
	if err := s.appCl.SetDetailedStatus(string(status)); err != nil {
		fmt.Printf("Failed to set status %v: %v\n", status, err)
	}
}

func (s *Server) setAppError(appErr error) {
	if err := s.appCl.SetError(appErr.Error()); err != nil {
		fmt.Printf("Failed to set error %v: %v\n", appErr, err)
	}
}

func (s *Server) sendServerErrHello(conn net.Conn, status HandshakeStatus) {
	sHello := ServerHello{
		Status: status,
	}

	if err := WriteJSON(conn, &sHello); err != nil {
		print(fmt.Sprintf("Error sending server hello: %v\n", err))
	}
}

func (s *Server) hasMultipleNetworkInterfaces(defaultNetworkInterface string) ([]string, bool) {
	networkInterfaces := strings.Split(defaultNetworkInterface, "\n")
	if len(networkInterfaces) > 1 {
		return networkInterfaces, true
	}
	return []string{}, false
}

func (s *Server) validateInterface(ifcs []string, selectedIfc string) bool {
	for _, ifc := range ifcs {
		if ifc == selectedIfc {
			return true
		}
	}
	return false
}
