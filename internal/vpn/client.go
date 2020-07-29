package vpn

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	ipv4FirstHalfAddr      = "0.0.0.0/1"
	ipv4SecondHalfAddr     = "128.0.0.0/1"
	directRouteNetmaskCIDR = "/32"
)

// Client is a VPN client.
type Client struct {
	cfg            ClientConfig
	log            logrus.FieldLogger
	conn           net.Conn
	directIPs      []net.IP
	defaultGateway net.IP
	closeC         chan struct{}
	closeOnce      sync.Once
}

// NewClient creates VPN client instance.
func NewClient(cfg ClientConfig, l logrus.FieldLogger, conn net.Conn) (*Client, error) {
	dmsgDiscIP, err := dmsgDiscIPFromEnv()
	if err != nil {
		return nil, fmt.Errorf("error getting Dmsg discovery IP: %w", err)
	}

	dmsgSrvAddrs, err := dmsgSrvAddrsFromEnv()
	if err != nil {
		return nil, fmt.Errorf("error getting Dmsg server addresses: %w", err)
	}

	tpDiscIP, err := tpDiscIPFromEnv()
	if err != nil {
		return nil, fmt.Errorf("error getting TP IP: %w", err)
	}

	arIP, err := addressResolverIPFromEnv()
	if err != nil {
		return nil, fmt.Errorf("error getting TP IP: %w", err)
	}

	rfIP, err := rfIPFromEnv()
	if err != nil {
		return nil, fmt.Errorf("error getting RF IP: %w", err)
	}

	stcpEntities, err := stcpEntitiesFromEnv()
	if err != nil {
		return nil, fmt.Errorf("error getting STCP entities: %w", err)
	}

	requiredDirectIPs := []net.IP{dmsgDiscIP, tpDiscIP, rfIP}
	directIPs := make([]net.IP, 0, len(requiredDirectIPs)+len(dmsgSrvAddrs)+len(stcpEntities))
	directIPs = append(directIPs, requiredDirectIPs...)
	directIPs = append(directIPs, dmsgSrvAddrs...)
	directIPs = append(directIPs, stcpEntities...)

	if arIP != nil {
		directIPs = append(directIPs, arIP)
	}

	defaultGateway, err := DefaultNetworkGateway()
	if err != nil {
		return nil, fmt.Errorf("error getting default network gateway: %w", err)
	}

	l.Infof("Got default network gateway IP: %s", defaultGateway)

	return &Client{
		cfg:            cfg,
		log:            l,
		conn:           conn,
		directIPs:      directIPs,
		defaultGateway: defaultGateway,
		closeC:         make(chan struct{}),
	}, nil
}

// Serve performs handshake with the server, sets up routing and starts handling traffic.
func (c *Client) Serve() error {
	tunIP, tunGateway, err := c.shakeHands()
	if err != nil {
		return fmt.Errorf("error during client/server handshake: %w", err)
	}

	c.log.Infof("Performed handshake with %s", c.conn.RemoteAddr())
	c.log.Infof("Local TUN IP: %s", tunIP.String())
	c.log.Infof("Local TUN gateway: %s", tunGateway.String())

	tun, err := newTUNDevice()
	if err != nil {
		return fmt.Errorf("error allocating TUN interface: %w", err)
	}
	defer func() {
		tunName := tun.Name()
		if err := tun.Close(); err != nil {
			c.log.WithError(err).Errorf("Error closing TUN %s", tunName)
		}
	}()

	c.log.Infof("Allocated TUN %s", tun.Name())

	if err := SetupTUN(tun.Name(), tunIP.String()+TUNNetmaskCIDR, tunGateway.String(), TUNMTU); err != nil {
		return fmt.Errorf("error setting up TUN %s: %w", tun.Name(), err)
	}

	defer c.removeDirectRoutes()
	if err := c.setupDirectRoutes(); err != nil {
		return fmt.Errorf("error setting up direct routes: %w", err)
	}

	if runtime.GOOS == "windows" {
		// okay, so, here's done because after the `SetupTUN` call,
		// interface doesn't get its values immediately. Reason is unknown,
		// all credits go to Microsoft. Delay may be different, this one is
		// fairly large to cover not really performant systems.
		time.Sleep(10 * time.Second)
	}

	defer c.routeTrafficDirectly(tunGateway)
	c.log.Infof("Routing all traffic through TUN %s", tun.Name())
	if err := c.routeTrafficThroughTUN(tunGateway); err != nil {
		return fmt.Errorf("error routing traffic through TUN %s: %w", tun.Name(), err)
	}

	connToTunDoneCh := make(chan struct{})
	tunToConnCh := make(chan struct{})
	// read all system traffic and pass it to the remote VPN server
	go func() {
		defer close(connToTunDoneCh)

		if _, err := io.Copy(tun, c.conn); err != nil {
			c.log.WithError(err).Errorf("Error resending traffic from TUN %s to VPN server", tun.Name())
		}
	}()
	go func() {
		defer close(tunToConnCh)

		if _, err := io.Copy(c.conn, tun); err != nil {
			c.log.WithError(err).Errorf("Error resending traffic from VPN server to TUN %s", tun.Name())
		}
	}()

	// only one side may fail here, so we wait till at least one fails
	select {
	case <-connToTunDoneCh:
	case <-tunToConnCh:
	case <-c.closeC:
	}

	return nil
}

// Close closes client.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.closeC)
	})
}

func (c *Client) routeTrafficThroughTUN(tunGateway net.IP) error {
	// route all traffic through TUN gateway
	if err := AddRoute(ipv4FirstHalfAddr, tunGateway.String()); err != nil {
		return err
	}
	if err := AddRoute(ipv4SecondHalfAddr, tunGateway.String()); err != nil {
		return err
	}

	return nil
}

func (c *Client) routeTrafficDirectly(tunGateway net.IP) {
	c.log.Infoln("Routing all traffic through default network gateway")

	// remove main route
	if err := DeleteRoute(ipv4FirstHalfAddr, tunGateway.String()); err != nil {
		c.log.WithError(err).Errorf("Error routing traffic through default network gateway")
	}
	if err := DeleteRoute(ipv4SecondHalfAddr, tunGateway.String()); err != nil {
		c.log.WithError(err).Errorf("Error routing traffic through default network gateway")
	}
}

func (c *Client) setupDirectRoutes() error {
	for _, ip := range c.directIPs {
		if !ip.IsLoopback() {
			c.log.Infof("Adding direct route to %s", ip.String())
			if err := AddRoute(ip.String()+directRouteNetmaskCIDR, c.defaultGateway.String()); err != nil {
				return fmt.Errorf("error adding direct route to %s", ip.String())
			}
		}
	}

	return nil
}

func (c *Client) removeDirectRoutes() {
	for _, ip := range c.directIPs {
		if !ip.IsLoopback() {
			c.log.Infof("Removing direct route to %s", ip.String())
			if err := DeleteRoute(ip.String()+directRouteNetmaskCIDR, c.defaultGateway.String()); err != nil {
				// shouldn't return, just keep on trying the other IPs
				c.log.WithError(err).Errorf("Error removing direct route to %s", ip.String())
			}
		}
	}
}

func dmsgDiscIPFromEnv() (net.IP, error) {
	return ipFromEnv(DmsgDiscAddrEnvKey)
}

func dmsgSrvAddrsFromEnv() ([]net.IP, error) {
	dmsgSrvCountStr := os.Getenv(DmsgAddrsCountEnvKey)
	if dmsgSrvCountStr == "" {
		return nil, errors.New("dmsg servers count is not provi")
	}
	dmsgSrvCount, err := strconv.Atoi(dmsgSrvCountStr)
	if err != nil {
		return nil, fmt.Errorf("invalid Dmsg servers count: %s: %w", dmsgSrvCountStr, err)
	}

	dmsgSrvAddrs := make([]net.IP, 0, dmsgSrvCount)
	for i := 0; i < dmsgSrvCount; i++ {
		dmsgSrvAddr, err := ipFromEnv(DmsgAddrEnvPrefix + strconv.Itoa(i))
		if err != nil {
			return nil, fmt.Errorf("error getting Dmsg server address: %w", err)
		}

		dmsgSrvAddrs = append(dmsgSrvAddrs, dmsgSrvAddr)
	}

	return dmsgSrvAddrs, nil
}

func tpDiscIPFromEnv() (net.IP, error) {
	return ipFromEnv(TPDiscAddrEnvKey)
}

func addressResolverIPFromEnv() (net.IP, error) {
	return ipFromEnv(AddressResolverAddrEnvKey)
}

func rfIPFromEnv() (net.IP, error) {
	return ipFromEnv(RFAddrEnvKey)
}

func stcpEntitiesFromEnv() ([]net.IP, error) {
	var stcpEntities []net.IP
	stcpTableLenStr := os.Getenv(STCPTableLenEnvKey)
	if stcpTableLenStr != "" {
		stcpTableLen, err := strconv.Atoi(stcpTableLenStr)
		if err != nil {
			return nil, fmt.Errorf("invalid STCP table len: %s: %w", stcpTableLenStr, err)
		}

		stcpEntities = make([]net.IP, 0, stcpTableLen)
		for i := 0; i < stcpTableLen; i++ {
			stcpKey := os.Getenv(STCPKeyEnvPrefix + strconv.Itoa(i))
			if stcpKey == "" {
				return nil, fmt.Errorf("env arg %s is not provided", STCPKeyEnvPrefix+strconv.Itoa(i))
			}

			stcpAddr, err := ipFromEnv(STCPValueEnvPrefix + stcpKey)
			if err != nil {
				return nil, fmt.Errorf("error getting STCP entity IP: %w", err)
			}

			stcpEntities = append(stcpEntities, stcpAddr)
		}
	}

	return stcpEntities, nil
}

func (c *Client) shakeHands() (TUNIP, TUNGateway net.IP, err error) {
	unavailableIPs, err := LocalNetworkInterfaceIPs()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting unavailable private IPs: %w", err)
	}

	unavailableIPs = append(unavailableIPs, c.defaultGateway)

	cHello := ClientHello{
		UnavailablePrivateIPs: unavailableIPs,
		Passcode:              c.cfg.Passcode,
	}

	c.log.Debugf("Sending client hello: %v", cHello)

	if err := WriteJSON(c.conn, &cHello); err != nil {
		return nil, nil, fmt.Errorf("error sending client hello: %w", err)
	}

	var sHello ServerHello
	if err := ReadJSON(c.conn, &sHello); err != nil {
		return nil, nil, fmt.Errorf("error reading server hello: %w", err)
	}

	c.log.Debugf("Got server hello: %v", sHello)

	if sHello.Status != HandshakeStatusOK {
		return nil, nil, fmt.Errorf("got status %d (%s) from the server", sHello.Status, sHello.Status)
	}

	return sHello.TUNIP, sHello.TUNGateway, nil
}

func ipFromEnv(key string) (net.IP, error) {
	ip, ok, err := IPFromEnv(key)
	if err != nil {
		return nil, fmt.Errorf("error getting IP from %s: %w", key, err)
	}
	if !ok {
		return nil, fmt.Errorf("env arg %s is not provided", key)
	}

	return ip, nil
}
