// Package vpn internal vpn
package vpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	ipv4FirstHalfAddr      = "0.0.0.0/1"
	ipv4SecondHalfAddr     = "128.0.0.0/1"
	directRouteNetmaskCIDR = "/32"
)

// Client is a VPN client.
type Client struct {
	cfg            ClientConfig
	appCl          *app.Client
	directIPSMu    sync.Mutex
	directIPs      []net.IP
	defaultGateway net.IP
	closeC         chan struct{}
	closeOnce      sync.Once

	prevTUNGateway   net.IP
	prevTUNGatewayMu sync.Mutex

	suidMu sync.Mutex //nolint
	suid   int        //nolint

	tunMu      sync.Mutex
	tun        TUNDevice
	tunCreated bool

	connectedDuration int64

	defaultSystemDNS string //nolint
}

// NewClient creates VPN client instance.
func NewClient(cfg ClientConfig, appCl *app.Client) (*Client, error) {
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

	utIP, err := uptimeTrackerIPFromEnv()
	if err != nil {
		return nil, fmt.Errorf("error getting UT IP: %w", err)
	}

	stcpEntities, err := stcpEntitiesFromEnv()
	if err != nil {
		return nil, fmt.Errorf("error getting Skywire-TCP entities: %w", err)
	}

	tpRemoteIPs, err := tpRemoteIPsFromEnv()
	if err != nil {
		return nil, fmt.Errorf("error getting TP remote IPs: %w", err)
	}

	requiredDirectIPs := []net.IP{dmsgDiscIP, tpDiscIP, rfIP}
	directIPs := make([]net.IP, 0, len(requiredDirectIPs)+len(dmsgSrvAddrs)+len(stcpEntities)+len(tpRemoteIPs))
	directIPs = append(directIPs, requiredDirectIPs...)
	directIPs = append(directIPs, dmsgSrvAddrs...)
	directIPs = append(directIPs, stcpEntities...)
	directIPs = append(directIPs, tpRemoteIPs...)

	if arIP != nil {
		directIPs = append(directIPs, arIP)
	}

	if utIP != nil {
		directIPs = append(directIPs, utIP)
	}

	defaultGateway, err := DefaultNetworkGateway()
	if err != nil {
		return nil, fmt.Errorf("error getting default network gateway: %w", err)
	}

	fmt.Printf("Got default network gateway IP: %s\n", defaultGateway)

	return &Client{
		cfg:            cfg,
		appCl:          appCl,
		directIPs:      filterOutEqualIPs(directIPs),
		defaultGateway: defaultGateway,
		closeC:         make(chan struct{}),
	}, nil
}

// Serve dials VPN server, sets up TUN and establishes VPN session.
func (c *Client) Serve() error {

	c.setAppStatus(appserver.AppDetailedStatusStarting)

	// we setup direct routes to skywire services once for all the client lifetime since routes don't change.
	// but if they change, new routes get delivered to the app via callbacks.
	if err := c.setupDirectRoutes(); err != nil {
		c.setAppError(err)
		return fmt.Errorf("error setting up direct routes: %w", err)
	}

	defer func() {
		c.removeDirectRoutes()
	}()

	// we call this preliminary, so it will be called on app stop
	defer func() {
		if c.cfg.Killswitch {
			c.prevTUNGatewayMu.Lock()
			if len(c.prevTUNGateway) > 0 {
				fmt.Printf("Routing traffic directly, previous TUN gateway: %s\n", c.prevTUNGateway.String())
				c.routeTrafficDirectly(c.prevTUNGateway)
			}
			c.prevTUNGateway = nil
			c.prevTUNGatewayMu.Unlock()
		}

		if err := c.closeTUN(); err != nil {
			print(fmt.Sprintf("Failed to close TUN: %v\n", err))
		}

		fmt.Println("Closing TUN")
	}()

	defer func() {
		c.setAppStatus(appserver.AppDetailedStatusShuttingDown)
		c.resetConnDuration()
	}()

	c.setAppStatus(appserver.AppDetailedStatusVPNConnecting)

	r := netutil.NewRetrier(nil, netutil.DefaultInitBackoff, netutil.DefaultMaxBackoff, 3, netutil.DefaultFactor).
		WithErrWhitelist(errHandshakeStatusForbidden, errHandshakeStatusInternalError, errHandshakeNoFreeIPs,
			errHandshakeStatusBadRequest, errNoTransportFound, errTransportNotFound, errErrSetupNode, errNotPermitted,
			errErrServerOffline)

	err := r.Do(context.Background(), func() error {
		if c.isClosed() {
			return nil
		}

		if err := c.dialServeConn(); err != nil {
			switch err {
			case errHandshakeStatusForbidden, errHandshakeStatusInternalError, errHandshakeNoFreeIPs,
				errHandshakeStatusBadRequest, errNoTransportFound, errTransportNotFound, errErrSetupNode, errNotPermitted,
				errErrServerOffline:
				c.setAppError(err)
				c.resetConnDuration()
				return err
			default:
				c.resetConnDuration()
				c.setAppStatus(appserver.AppDetailedStatusReconnecting)
				c.setAppError(errTimeout)
				fmt.Println("\nConnection broke, reconnecting...")
				return fmt.Errorf("dialServeConn: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to connect to the server: %w", err)
	}

	return nil
}

// ListenIPC starts named-pipe based connection server for windows or unix socket in Linux/Mac
func (c *Client) ListenIPC(client *ipc.Client) {
	if client == nil {
		print(fmt.Sprintln("Unable to create IPC Client: server is non-existent"))
		return
	}
	for {
		m, err := client.Read()
		if err != nil {
			print(fmt.Sprintf("%s IPC received error: %v\n", skyenv.VPNClientName, err))
		}

		if m != nil {
			if m.MsgType == skyenv.IPCShutdownMessageType {
				fmt.Println("Stopping " + skyenv.VPNClientName + " via IPC")
				break
			}
		}

	}
	client.Close()
	c.Close()
}

// Close closes client.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.closeC)
	})
}

// AddDirectRoute adds new direct route. Packets destined to `ip` will
// go directly, ignoring VPN.
func (c *Client) AddDirectRoute(ip net.IP) error {
	c.directIPSMu.Lock()
	defer c.directIPSMu.Unlock()

	for _, storedIP := range c.directIPs {
		if ip.Equal(storedIP) {
			return nil
		}
	}

	c.directIPs = append(c.directIPs, ip)

	return c.setupDirectRoute(ip)
}

func (c *Client) removeDirectRouteFn(ip net.IP, i int) error {
	c.directIPs = append(c.directIPs[:i], c.directIPs[i+1:]...)

	return c.removeDirectRoute(ip)
}

// RemoveDirectRoute removes direct route. Packets destined to `ip` will
// go through VPN.
func (c *Client) RemoveDirectRoute(ip net.IP) error {
	c.directIPSMu.Lock()
	defer c.directIPSMu.Unlock()

	for i, storedIP := range c.directIPs {
		if ip.Equal(storedIP) {
			if err := c.removeDirectRouteFn(ip, i); err != nil {
				return err
			}
			break
		}
	}

	return nil
}

func (c *Client) setSysPrivileges() error { //nolint
	if runtime.GOOS != "windows" {
		c.suidMu.Lock()

		// we don't release the lock here to avoid races,
		// lock will be released after reverting system privileges

		suid, err := setupClientSysPrivileges()
		if err != nil {
			return err
		}

		c.suid = suid
	}

	return nil
}

func (c *Client) createTUN() (TUNDevice, error) {
	c.tunMu.Lock()
	defer c.tunMu.Unlock()

	if c.tunCreated {
		return c.tun, nil
	}

	tun, err := newTUNDevice()
	if err != nil {
		return nil, err
	}

	c.tun = tun
	c.tunCreated = true

	return tun, nil
}

func (c *Client) closeTUN() error {
	c.tunMu.Lock()
	defer c.tunMu.Unlock()

	if !c.tunCreated {
		return nil
	}

	c.tunCreated = false

	c.RevertDNS()

	return c.tun.Close()
}

func (c *Client) setupTUN(tunIP, tunGateway net.IP) error {
	c.tunMu.Lock()
	defer c.tunMu.Unlock()

	if !c.tunCreated {
		return errors.New("TUN is not created")
	}

	return c.SetupTUN(c.tun.Name(), tunIP.String()+TUNNetmaskCIDR, tunGateway.String(), TUNMTU)
}

func (c *Client) serveConn(conn net.Conn) error {
	tunIP, tunGateway, err := c.shakeHands(conn)
	if err != nil {
		fmt.Printf("error during client/server handshake: %s\n", err)
		return err
	}

	fmt.Printf("Performed handshake with %s\n", conn.RemoteAddr())
	fmt.Printf("Local TUN IP: %s\n", tunIP.String())
	fmt.Printf("Local TUN gateway: %s\n", tunGateway.String())

	fmt.Println("CREATING TUN INTERFACE")
	tun, err := c.createTUN()
	if err != nil {
		return fmt.Errorf("error allocating TUN interface: %w", err)
	}

	// we don't defer TUN closing routine here, so that the interface might be
	// reused. this function may end in 2 cases: connection failure, app stop.
	// in case app got stopped, TUN will be closed in the outer code, while in
	// case of connection failure it will be reused

	fmt.Printf("Allocated TUN %s: %v\n", tun.Name(), err)

	fmt.Printf("Setting up TUN device with: %s and Gateway %s\n", tunIP, tunGateway)
	if err := c.setupTUN(tunIP, tunGateway); err != nil {
		return fmt.Errorf("error setting up TUN %s: %w", tun.Name(), err)
	}

	if runtime.GOOS == "windows" {
		// okay, so, here's done because after the `SetupTUN` call,
		// interface doesn't get its values immediately. Reason is unknown,
		// all credits go to Microsoft. Delay may be different, this one is
		// fairly large to cover not really performant systems.
		time.Sleep(13 * time.Second)
	}

	fmt.Printf("TUN %s all sets\n", tunIP)

	isNewRoute := true
	if c.cfg.Killswitch {
		c.prevTUNGatewayMu.Lock()
		if len(c.prevTUNGateway) > 0 {
			isNewRoute = false
		}
		c.prevTUNGateway = tunGateway
		c.prevTUNGatewayMu.Unlock()
	}

	fmt.Printf("Routing all traffic through TUN %s: %v\n", tun.Name(), err)
	if err := c.routeTrafficThroughTUN(tunGateway, isNewRoute); err != nil {
		return fmt.Errorf("error routing traffic through TUN %s: %w", tun.Name(), err)
	}

	c.setAppStatus(appserver.AppDetailedStatusRunning)
	c.resetConnDuration()
	t := time.NewTicker(time.Second)

	defer func() {
		if !c.cfg.Killswitch {
			fmt.Println("serveConn done, killswitch disabled, routing traffic directly")
			c.routeTrafficDirectly(tunGateway)
		}
	}()

	// we release privileges here (user is not root for Mac OS systems from here on)

	connToTunDoneCh := make(chan struct{})
	tunToConnCh := make(chan struct{})
	// read all system traffic and pass it to the remote VPN server
	go func() {
		defer close(connToTunDoneCh)

		if _, err := io.Copy(tun, conn); err != nil {
			if !c.isClosed() {
				print(fmt.Sprintf("Error resending traffic from TUN %s to VPN server: %v\n", tun.Name(), err))
				// when the vpn-server is closed we get the error EOF
				if err.Error() == io.EOF.Error() {
					c.setAppError(errVPNServerClosed)
				}
			}
		}
	}()
	go func() {
		defer close(tunToConnCh)

		if _, err := io.Copy(conn, tun); err != nil {
			if !c.isClosed() {
				print(fmt.Sprintf("Error resending traffic from VPN server to TUN %s: %v\n", tun.Name(), err))
			}
		}
	}()

	// only one side may fail here, so we wait till at least one fails
serveLoop:
	for {
		select {
		case <-connToTunDoneCh:
			break serveLoop
		case <-tunToConnCh:
			break serveLoop
		case <-c.closeC:
			break serveLoop
		case <-t.C:
			atomic.AddInt64(&c.connectedDuration, 1)
			c.setConnectionDuration()
		}
	}

	return nil
}

func (c *Client) dialServeConn() error {
	conn, err := c.dialServer(c.appCl, c.cfg.ServerPK)
	if err != nil {
		fmt.Printf("error connecting to VPN server: %s\n", err)
		return err
	}
	fmt.Printf("Dialed %s\n", conn.RemoteAddr())

	defer func() {
		if err := conn.Close(); err != nil {
			print(fmt.Sprintf("Error closing app conn: %v\n", err))
		}
	}()

	if c.isClosed() {
		return nil
	}

	if err := c.serveConn(conn); err != nil {
		fmt.Printf("error serving app conn: %s", err)
		return err
	}

	return nil
}

func (c *Client) routeTrafficThroughTUN(tunGateway net.IP, isNewRoute bool) error {
	// route all traffic through TUN gateway
	if isNewRoute {
		if err := c.AddRoute(ipv4FirstHalfAddr, tunGateway.String()); err != nil {
			return err
		}
		if err := c.AddRoute(ipv4SecondHalfAddr, tunGateway.String()); err != nil {
			return err
		}
	} else {
		if err := c.ChangeRoute(ipv4FirstHalfAddr, tunGateway.String()); err != nil {
			return err
		}
		if err := c.ChangeRoute(ipv4SecondHalfAddr, tunGateway.String()); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) routeTrafficDirectly(tunGateway net.IP) {
	fmt.Println("Routing all traffic through default network gateway")

	// remove main route
	if err := c.DeleteRoute(ipv4FirstHalfAddr, tunGateway.String()); err != nil {
		print(fmt.Sprintf("Error routing traffic through default network gateway: %v\n", err))
	}
	if err := c.DeleteRoute(ipv4SecondHalfAddr, tunGateway.String()); err != nil {
		print(fmt.Sprintf("Error routing traffic through default network gateway: %v\n", err))
	}
}

func (c *Client) setupDirectRoutes() error {
	c.directIPSMu.Lock()
	defer c.directIPSMu.Unlock()

	for _, ip := range c.directIPs {
		if err := c.setupDirectRoute(ip); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) setupDirectRoute(ip net.IP) error {
	if !ip.IsLoopback() {
		fmt.Printf("Adding direct route to %s, via %s\n", ip.String(), c.defaultGateway.String())
		if err := c.AddRoute(ip.String()+directRouteNetmaskCIDR, c.defaultGateway.String()); err != nil {
			return fmt.Errorf("error adding direct route to %s: %w", ip.String(), err)
		}
	}

	return nil
}

func (c *Client) removeDirectRoute(ip net.IP) error {
	if !ip.IsLoopback() {
		fmt.Printf("Removing direct route to %s\n", ip.String())
		if err := c.DeleteRoute(ip.String()+directRouteNetmaskCIDR, c.defaultGateway.String()); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) removeDirectRoutes() {
	c.directIPSMu.Lock()
	defer c.directIPSMu.Unlock()

	for _, ip := range c.directIPs {
		if err := c.removeDirectRoute(ip); err != nil {
			// shouldn't return, just keep on trying the other IPs
			print(fmt.Sprintf("Error removing direct route to %s: %v\n", ip.String(), err))
		}
	}
}

func dmsgDiscIPFromEnv() (net.IP, error) {
	return ipFromEnv(DmsgDiscAddrEnvKey)
}

func dmsgSrvAddrsFromEnv() ([]net.IP, error) {
	dmsgSrvCountStr := os.Getenv(DmsgAddrsCountEnvKey)
	if dmsgSrvCountStr == "" {
		return nil, errors.New("dmsg servers count is not provided")
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

func uptimeTrackerIPFromEnv() (net.IP, error) {
	return ipFromEnv(UptimeTrackerAddrEnvKey)
}

func tpRemoteIPsFromEnv() ([]net.IP, error) {
	var ips []net.IP
	ipsLenStr := os.Getenv(TPRemoteIPsLenEnvKey)
	if ipsLenStr == "" {
		return nil, nil
	}

	ipsLen, err := strconv.Atoi(ipsLenStr)
	if err != nil {
		return nil, fmt.Errorf("invalid TPs remote IPs len: %s: %w", ipsLenStr, err)
	}

	ips = make([]net.IP, 0, ipsLen)
	for i := 0; i < ipsLen; i++ {
		key := TPRemoteIPsEnvPrefix + strconv.Itoa(i)

		ipStr := os.Getenv(key)
		if ipStr == "" {
			return nil, fmt.Errorf("env arg %s is not provided", key)
		}

		ip, err := ipFromEnv(key)
		if err != nil {
			return nil, fmt.Errorf("error getting TP remote IP: %w", err)
		}

		ips = append(ips, ip)
	}

	return ips, nil
}

func stcpEntitiesFromEnv() ([]net.IP, error) {
	var stcpEntities []net.IP
	stcpTableLenStr := os.Getenv(STCPTableLenEnvKey)
	if stcpTableLenStr != "" {
		stcpTableLen, err := strconv.Atoi(stcpTableLenStr)
		if err != nil {
			return nil, fmt.Errorf("invalid Skywire-TCP table len: %s: %w", stcpTableLenStr, err)
		}

		stcpEntities = make([]net.IP, 0, stcpTableLen)
		for i := 0; i < stcpTableLen; i++ {
			stcpKey := os.Getenv(STCPKeyEnvPrefix + strconv.Itoa(i))
			if stcpKey == "" {
				return nil, fmt.Errorf("env arg %s is not provided", STCPKeyEnvPrefix+strconv.Itoa(i))
			}

			stcpAddr, err := ipFromEnv(STCPValueEnvPrefix + stcpKey)
			if err != nil {
				return nil, fmt.Errorf("error getting Skywire-TCP entity IP: %w", err)
			}

			stcpEntities = append(stcpEntities, stcpAddr)
		}
	}

	return stcpEntities, nil
}

func (c *Client) shakeHands(conn net.Conn) (TUNIP, TUNGateway net.IP, err error) {
	unavailableIPs, err := netutil.LocalNetworkInterfaceIPs()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting unavailable private IPs: %w", err)
	}

	unavailableIPs = append(unavailableIPs, c.defaultGateway)

	cHello := ClientHello{
		UnavailablePrivateIPs: unavailableIPs,
		Passcode:              c.cfg.Passcode,
	}

	const handshakeTimeout = 5 * time.Second

	fmt.Printf("Sending client hello: %v\n", cHello)

	if err := WriteJSONWithTimeout(conn, &cHello, handshakeTimeout); err != nil {
		return nil, nil, fmt.Errorf("error sending client hello: %w", err)
	}

	var sHello ServerHello
	if err := ReadJSONWithTimeout(conn, &sHello, handshakeTimeout); err != nil {
		fmt.Printf("error reading server hello: %v\n", err)
		if strings.Contains(err.Error(), appnet.ErrServiceOffline(skyenv.VPNServerPort).Error()) {
			err = appserver.RPCErr{
				Err: err.Error(),
			}
		}
		return nil, nil, err
	}

	fmt.Printf("Got server hello: %v", sHello)

	if sHello.Status != HandshakeStatusOK {
		return nil, nil, sHello.Status.getError()
	}

	return sHello.TUNIP, sHello.TUNGateway, nil
}

func (c *Client) dialServer(appCl *app.Client, pk cipher.PubKey) (net.Conn, error) {
	const (
		netType = appnet.TypeSkynet
		vpnPort = routing.Port(skyenv.VPNServerPort)
	)

	var conn net.Conn
	var err error
	conn, err = appCl.Dial(appnet.Addr{
		Net:    netType,
		PubKey: pk,
		Port:   vpnPort,
	})

	if err != nil {
		return nil, err
	}

	if c.isClosed() {
		// we need to signal outer code that connection object is invalid
		// in this case
		return nil, errors.New("client got closed")
	}

	return conn, nil
}

func (c *Client) setAppStatus(status appserver.AppDetailedStatus) {
	if err := c.appCl.SetDetailedStatus(string(status)); err != nil {
		print(fmt.Sprintf("Failed to set status %v: %v\n", status, err))
	}
}

func (c *Client) setConnectionDuration() {
	if err := c.appCl.SetConnectionDuration(atomic.LoadInt64(&c.connectedDuration)); err != nil {
		print(fmt.Sprintf("Failed to set connection duration: %v\n", err))
	}
}

func (c *Client) setAppError(appErr error) {
	if err := c.appCl.SetError(appErr.Error()); err != nil {
		print(fmt.Sprintf("Failed to set error %v: %v\n", appErr, err))
	}
}

func (c *Client) isClosed() bool {
	select {
	case <-c.closeC:
		return true
	default:
	}

	return false
}

func (c *Client) resetConnDuration() {
	atomic.StoreInt64(&c.connectedDuration, 0)
	c.setConnectionDuration()
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

func filterOutEqualIPs(ips []net.IP) []net.IP {
	ipsSet := make(map[string]struct{})
	var filteredIPs []net.IP
	for _, ip := range ips {
		if ip != nil {
			ipStr := ip.String()

			if _, ok := ipsSet[ipStr]; !ok {
				filteredIPs = append(filteredIPs, ip)
				ipsSet[ip.String()] = struct{}{}
			}
		}
	}

	return filteredIPs
}
