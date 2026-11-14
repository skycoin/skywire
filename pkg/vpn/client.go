// Package vpn pkg/vpn/client.go c3-app-vpn
package vpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ipc "github.com/0magnet/golang-ipc"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
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
	cfg         ClientConfig
	log         logging.FieldLogger
	appCl       *app.Client
	directIPSMu sync.Mutex
	directIPs   []netip.Addr // every IP with a direct route installed
	// pinned are the IPs the visor named at start (services, the sessions
	// and transports up then): their routes stay for the client's life.
	// refs counts the live dial events per IP, so the route of an IP two
	// transports share survives one of them closing.
	pinned map[netip.Addr]bool
	refs   map[netip.Addr]int
	// addDirect and delDirect install and remove one route; tests swap
	// them to leave the OS routing table alone.
	addDirect, delDirect func(netip.Addr) error
	defaultGateway       netip.Addr
	// gatewayFn looks the default gateway up again after a network move.
	gatewayFn func() (netip.Addr, error)
	closeC    chan struct{}
	closeOnce sync.Once

	prevTUNGateway   netip.Addr
	prevTUNGatewayMu sync.Mutex

	suidMu sync.Mutex //nolint:unused
	suid   int        //nolint:unused

	tunMu      sync.Mutex
	tun        TUNDevice
	tunCreated bool

	connectedDuration atomic.Int64

	defaultSystemDNS string //nolint:unused

	// sharing is set once, before the first session, on builds that pass
	// other devices' connections into the tunnel (see share.go). share is
	// the live session as that proxy sees it; nil between sessions.
	sharing bool
	share   atomic.Pointer[shareSession]

	// mesh is the running mesh gateway, nil when it is off. Set before the
	// first session.
	mesh *meshClientGateway
}

// NewClient creates VPN client instance.
func NewClient(cfg ClientConfig, appCl *app.Client) (*Client, error) {
	var directIPs []netip.Addr
	// A browser visor's netstack has no routes for anything to go around.
	if runtime.GOOS != "js" || cfg.Host {
		var err error
		if directIPs, err = directIPsFromEnv(); err != nil {
			return nil, err
		}
	}

	gatewayFn := DefaultNetworkGateway
	if cfg.Host {
		gatewayFn = hostGateway
	}
	defaultGateway, err := gatewayFn()
	if err != nil {
		return nil, fmt.Errorf("error getting default network gateway: %w", err)
	}

	c := &Client{
		cfg:            cfg,
		log:            cfg.Log,
		appCl:          appCl,
		directIPs:      filterOutEqualIPs(directIPs),
		defaultGateway: defaultGateway,
		gatewayFn:      gatewayFn,
		closeC:         make(chan struct{}),
	}
	c.logger().Infof("Got default network gateway IP: %s", defaultGateway)
	c.initDirectRoutes()
	return c, nil
}

// logger is the configured logger, or the vpn_client module logger when none
// was set (as in tests that build a Client literal).
func (c *Client) logger() logging.FieldLogger {
	if c.log == nil {
		return logging.MustGetLogger("vpn_client")
	}
	return c.log
}

// directIPsFromEnv is every IP the visor named for the tunnel to route
// around: its services, dmsg servers and transports.
func directIPsFromEnv() ([]netip.Addr, error) {
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
		return nil, fmt.Errorf("error getting Skywire-TCP entities: %w", err)
	}

	tpRemoteIPs, err := tpRemoteIPsFromEnv()
	if err != nil {
		return nil, fmt.Errorf("error getting TP remote IPs: %w", err)
	}

	requiredDirectIPs := []netip.Addr{dmsgDiscIP, tpDiscIP, rfIP}
	directIPs := make([]netip.Addr, 0, len(requiredDirectIPs)+len(dmsgSrvAddrs)+len(stcpEntities)+len(tpRemoteIPs))
	directIPs = append(directIPs, requiredDirectIPs...)
	directIPs = append(directIPs, dmsgSrvAddrs...)
	directIPs = append(directIPs, stcpEntities...)
	directIPs = append(directIPs, tpRemoteIPs...)

	if arIP.IsValid() {
		directIPs = append(directIPs, arIP)
	}
	return directIPs, nil
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

	// Other devices' connections, when this build has a way to receive them
	// (the phone's hotspot). They wait for a tunnel session and die with it.
	if stop := c.startSharing(); stop != nil {
		defer stop()
	}

	// Optional mesh gateway: let the host resolve *.dmsg / *.skynet by name and
	// proxy those over the mesh (bypassing the tunnel). Opt-in; Linux and Android.
	if c.cfg.MeshGateway {
		meshGW, err := c.startMeshGateway()
		if err != nil {
			// Non-fatal: the VPN still works, just without mesh-name resolution.
			c.logger().WithError(err).Warn("Mesh gateway disabled")
		} else {
			c.mesh = meshGW
			defer meshGW.stop()
		}
	}

	// we call this preliminary, so it will be called on app stop
	defer func() {
		if c.cfg.Killswitch {
			c.prevTUNGatewayMu.Lock()
			if c.prevTUNGateway.IsValid() {
				c.logger().Infof("Routing traffic directly, previous TUN gateway: %s", c.prevTUNGateway.String())
				c.routeTrafficDirectly(c.prevTUNGateway)
			}
			c.prevTUNGateway = netip.Addr{}
			c.prevTUNGatewayMu.Unlock()
		}

		if err := c.closeTUN(); err != nil {
			c.logger().WithError(err).Error("Failed to close TUN")
		}

		c.logger().Info("Closing TUN")
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
				c.logger().Warn("Connection broke, reconnecting...")
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
		c.logger().Error("Unable to create IPC Client: server is non-existent")
		return
	}
	for {
		m, err := client.Read()
		if err != nil {
			c.logger().WithError(err).Errorf("%s IPC received error", skyenv.VPNClientName)
		}

		if m != nil {
			if m.MsgType == skyenv.IPCShutdownMessageType {
				c.logger().Infof("Stopping %s via IPC", skyenv.VPNClientName)
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

// initDirectRoutes pins the start-time IPs and wires the route
// operations.
func (c *Client) initDirectRoutes() {
	c.pinned = make(map[netip.Addr]bool, len(c.directIPs))
	for _, ip := range c.directIPs {
		c.pinned[ip] = true
	}
	c.refs = make(map[netip.Addr]int)
	if c.addDirect == nil {
		c.addDirect = c.setupDirectRoute
	}
	if c.delDirect == nil {
		c.delDirect = c.removeDirectRoute
	}
}

// AddDirectRoute counts one more user of a direct route to ip — packets
// to it go directly, ignoring the VPN — installing it on the first.
func (c *Client) AddDirectRoute(ip netip.Addr) error {
	ip = ip.Unmap()
	c.directIPSMu.Lock()
	defer c.directIPSMu.Unlock()

	c.refs[ip]++
	if slices.Contains(c.directIPs, ip) {
		return nil
	}
	c.directIPs = append(c.directIPs, ip)
	return c.addDirect(ip)
}

// RemoveDirectRoute counts one user fewer, removing the route when none
// is left — unless the visor named the IP at start, whose route stays.
func (c *Client) RemoveDirectRoute(ip netip.Addr) error {
	ip = ip.Unmap()
	c.directIPSMu.Lock()
	defer c.directIPSMu.Unlock()

	if c.refs[ip] > 0 {
		c.refs[ip]--
	}
	if c.refs[ip] > 0 || c.pinned[ip] {
		return nil
	}
	delete(c.refs, ip)
	for i, storedIP := range c.directIPs {
		if ip == storedIP {
			c.directIPs = append(c.directIPs[:i], c.directIPs[i+1:]...)
			return c.delDirect(ip)
		}
	}
	return nil
}

func (c *Client) setSysPrivileges() error { //nolint:unused
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

	if c.tunCreated && !deadTUN(c.tun) {
		return c.tun, nil
	}

	newTUN := newTUNDevice
	if c.cfg.Host {
		newTUN = hostTUN
	}
	tun, err := newTUN()
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

func (c *Client) setupTUN(tunIP, tunGateway netip.Addr) error {
	c.tunMu.Lock()
	defer c.tunMu.Unlock()

	if !c.tunCreated {
		return errors.New("TUN is not created")
	}

	return c.SetupTUN(c.tun.Name(), tunIP.String()+TUNNetmaskCIDR, tunGateway.String(), TUNMTU)
}

// nolint: gocyclo
//
//gocyclo:ignore
func (c *Client) serveConn(conn net.Conn) error {
	tunIP, tunGateway, err := c.shakeHands(conn)
	if err != nil {
		c.logger().WithError(err).Error("Error during client/server handshake")
		return err
	}

	c.logger().Infof("Performed handshake with %s", conn.RemoteAddr())
	c.logger().Infof("Local TUN IP: %s", tunIP.String())
	c.logger().Infof("Local TUN gateway: %s", tunGateway.String())

	c.logger().Debug("Creating TUN interface")
	tun, err := c.createTUN()
	if err != nil {
		return fmt.Errorf("error allocating TUN interface: %w", err)
	}

	// we don't defer TUN closing routine here, so that the interface might be
	// reused. this function may end in 2 cases: connection failure, app stop.
	// in case app got stopped, TUN will be closed in the outer code, while in
	// case of connection failure it will be reused

	c.logger().Infof("Allocated TUN %s", tun.Name())

	c.logger().Debugf("Setting up TUN device with: %s and Gateway %s", tunIP, tunGateway)
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

	c.logger().Infof("TUN %s all set", tunIP)

	newRoute := true
	if c.cfg.Killswitch {
		c.prevTUNGatewayMu.Lock()
		if c.prevTUNGateway.IsValid() {
			newRoute = false
		}
		c.prevTUNGateway = tunGateway
		c.prevTUNGatewayMu.Unlock()
	}

	c.logger().Infof("Routing all traffic through TUN %s", tun.Name())
	if err := c.routeTrafficThroughTUN(tunGateway, newRoute); err != nil {
		return fmt.Errorf("error routing traffic through TUN %s: %w", tun.Name(), err)
	}

	c.setAppStatus(appserver.AppDetailedStatusRunning)
	c.resetConnDuration()
	t := time.NewTicker(time.Second)
	defer t.Stop()

	defer func() {
		if !c.cfg.Killswitch {
			c.logger().Info("serveConn done, killswitch disabled, routing traffic directly")
			c.routeTrafficDirectly(tunGateway)
		}
	}()

	// we release privileges here (user is not root for Mac OS systems from here on)

	// With sharing on, the conn has a second writer — the shared netstack —
	// and the server's replies to it are picked out on the way in.
	// SkyDNS's own lookups ride the share session too.
	inbound, outbound := io.Writer(tun), io.Writer(conn)
	sky := c.mesh.engine()
	if c.sharing || sky != nil {
		lw := &lockedWriter{w: conn}
		sess := newShareSession(tunIP, lw.Write)
		c.share.Store(sess)
		defer func() {
			c.share.CompareAndSwap(sess, nil)
			sess.close()
		}()
		inbound, outbound = shareDemux{tun: tun, sess: sess}, lw
	}
	if sky != nil {
		outbound = skyDNSSplit{sky: sky, next: outbound}
	}

	connToTunDoneCh := make(chan struct{})
	tunToConnCh := make(chan struct{})
	// read all system traffic and pass it to the remote VPN server
	go func() {
		defer close(connToTunDoneCh)

		if _, err := io.Copy(inbound, conn); err != nil {
			if !c.isClosed() {
				c.logger().WithError(err).Errorf("Error resending traffic from TUN %s to VPN server", tun.Name())
				// when the vpn-server is closed we get the error EOF
				if err.Error() == io.EOF.Error() {
					c.setAppError(errVPNServerClosed)
				}
			}
		}
	}()
	go func() {
		defer close(tunToConnCh)

		if _, err := io.Copy(outbound, tun); err != nil {
			if !c.isClosed() {
				c.logger().WithError(err).Errorf("Error resending traffic from VPN server to TUN %s", tun.Name())
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
			c.connectedDuration.Add(1)
			c.setConnectionDuration()
		}
	}

	return nil
}

func (c *Client) dialServeConn() error {
	conn, err := c.dialServer(c.appCl, c.cfg.ServerPK)
	if err != nil {
		c.logger().WithError(err).Error("Error connecting to VPN server")
		return err
	}
	c.logger().Infof("Dialed %s", conn.RemoteAddr())

	defer func() {
		if err := conn.Close(); err != nil {
			c.logger().WithError(err).Warn("Error closing app conn")
		}
	}()

	if c.isClosed() {
		return nil
	}

	if err := c.serveConn(conn); err != nil {
		c.logger().WithError(err).Error("Error serving app conn")
		return err
	}

	return nil
}

func (c *Client) routeTrafficThroughTUN(tunGateway netip.Addr, newRoute bool) error {
	// route all traffic through TUN gateway
	if newRoute {
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

func (c *Client) routeTrafficDirectly(tunGateway netip.Addr) {
	c.logger().Info("Routing all traffic through default network gateway")

	// remove main route
	if err := c.DeleteRoute(ipv4FirstHalfAddr, tunGateway.String()); err != nil {
		c.logger().WithError(err).Error("Error routing traffic through default network gateway")
	}
	if err := c.DeleteRoute(ipv4SecondHalfAddr, tunGateway.String()); err != nil {
		c.logger().WithError(err).Error("Error routing traffic through default network gateway")
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

func (c *Client) setupDirectRoute(ip netip.Addr) error {
	if !ip.IsLoopback() {
		c.logger().Infof("Adding direct route to %s, via %s", ip.String(), c.defaultGateway.String())
		if err := c.AddRoute(ip.String()+directRouteNetmaskCIDR, c.defaultGateway.String()); err != nil {
			return fmt.Errorf("error adding direct route to %s: %w", ip.String(), err)
		}
	}

	return nil
}

func (c *Client) removeDirectRoute(ip netip.Addr) error {
	if !ip.IsLoopback() {
		c.logger().Infof("Removing direct route to %s", ip.String())
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
			c.logger().WithError(err).Warnf("Error removing direct route to %s", ip.String())
		}
	}
}

func dmsgDiscIPFromEnv() (netip.Addr, error) {
	return optionalIPFromEnv(DmsgDiscAddrEnvKey)
}

func dmsgSrvAddrsFromEnv() ([]netip.Addr, error) {
	dmsgSrvCountStr := os.Getenv(DmsgAddrsCountEnvKey)
	if dmsgSrvCountStr == "" {
		return nil, errors.New("dmsg servers count is not provided")
	}
	dmsgSrvCount, err := strconv.Atoi(dmsgSrvCountStr)
	if err != nil {
		return nil, fmt.Errorf("invalid Dmsg servers count: %s: %w", dmsgSrvCountStr, err)
	}

	dmsgSrvAddrs := make([]netip.Addr, 0, dmsgSrvCount)
	for i := range dmsgSrvCount {
		dmsgSrvAddr, err := ipFromEnv(DmsgAddrEnvPrefix + strconv.Itoa(i))
		if err != nil {
			return nil, fmt.Errorf("error getting Dmsg server address: %w", err)
		}

		dmsgSrvAddrs = append(dmsgSrvAddrs, dmsgSrvAddr)
	}

	return dmsgSrvAddrs, nil
}

func tpDiscIPFromEnv() (netip.Addr, error) {
	return optionalIPFromEnv(TPDiscAddrEnvKey)
}

func addressResolverIPFromEnv() (netip.Addr, error) {
	return optionalIPFromEnv(AddressResolverAddrEnvKey)
}

func rfIPFromEnv() (netip.Addr, error) {
	return optionalIPFromEnv(RFAddrEnvKey)
}

func tpRemoteIPsFromEnv() ([]netip.Addr, error) {
	var ips []netip.Addr
	ipsLenStr := os.Getenv(TPRemoteIPsLenEnvKey)
	if ipsLenStr == "" {
		return nil, nil
	}

	ipsLen, err := strconv.Atoi(ipsLenStr)
	if err != nil {
		return nil, fmt.Errorf("invalid TPs remote IPs len: %s: %w", ipsLenStr, err)
	}

	ips = make([]netip.Addr, 0, ipsLen)
	for i := range ipsLen {
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

func stcpEntitiesFromEnv() ([]netip.Addr, error) {
	var stcpEntities []netip.Addr
	stcpTableLenStr := os.Getenv(STCPTableLenEnvKey)
	if stcpTableLenStr != "" {
		stcpTableLen, err := strconv.Atoi(stcpTableLenStr)
		if err != nil {
			return nil, fmt.Errorf("invalid Skywire-TCP table len: %s: %w", stcpTableLenStr, err)
		}

		stcpEntities = make([]netip.Addr, 0, stcpTableLen)
		for i := range stcpTableLen {
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

func (c *Client) shakeHands(conn net.Conn) (TUNIP, TUNGateway netip.Addr, err error) {
	localIPs, err := netutil.LocalNetworkInterfaceIPs()
	if err != nil {
		if c.appCl != nil {
			return netip.Addr{}, netip.Addr{}, fmt.Errorf("error getting unavailable private IPs: %w", err)
		}
		// Embedded client (NewClientEmbedded — the browser): there are no host
		// NICs to protect from the server's address allocation, and js has no
		// interface enumeration to consult. An empty set is the truth.
		localIPs = nil
	}
	unavailableIPs := make([]netip.Addr, 0, len(localIPs)+1)
	for _, ip := range localIPs {
		if a, ok := netip.AddrFromSlice(ip); ok {
			unavailableIPs = append(unavailableIPs, a.Unmap())
		}
	}

	// Only a real gateway address belongs in the reserve list: the embedded
	// client (browser) has none, and a nil/unspecified IP in the hello makes
	// the server's ipGen.Reserve fail the whole handshake as malformed.
	if gw := c.gateway(); gw.IsValid() && !gw.IsUnspecified() {
		unavailableIPs = append(unavailableIPs, gw)
	}

	cHello := ClientHello{
		UnavailablePrivateIPs: unavailableIPs,
	}

	const handshakeTimeout = 5 * time.Second

	c.logger().Debugf("Sending client hello: %v", cHello)

	if err := WriteJSONWithTimeout(conn, &cHello, handshakeTimeout); err != nil {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("error sending client hello: %w", err)
	}

	var sHello ServerHello
	if err := ReadJSONWithTimeout(conn, &sHello, handshakeTimeout); err != nil {
		c.logger().WithError(err).Error("Error reading server hello")
		if strings.Contains(err.Error(), appnet.ErrServiceOffline(skyenv.VPNServerPort).Error()) {
			err = appserver.RPCErr{
				Err: err.Error(),
			}
		}
		return netip.Addr{}, netip.Addr{}, err
	}

	c.logger().Debugf("Got server hello: %v", sHello)

	if sHello.Status != HandshakeStatusOK {
		return netip.Addr{}, netip.Addr{}, sHello.Status.getError()
	}

	return sHello.TUNIP, sHello.TUNGateway, nil
}

func (c *Client) dialServer(appCl *app.Client, pk cipher.PubKey) (net.Conn, error) {
	const (
		netType    = appnet.TypeSkynet
		serverPort = routing.Port(skyenv.VPNServerPort) // VPN server port (44)
	)

	// dial one network to the server. On skynet, honor the per-call route options
	// (mux/min-hops) when set; dmsg is a plain relay stream with no such options.
	dial := func(_ context.Context, a appnet.Addr) (net.Conn, error) {
		if a.Net == netType && (c.cfg.MuxRoutes > 1 || c.cfg.MinHops >= 2) {
			return appCl.DialWithOptions(a, appserver.DialOptionsReq{MuxRoutes: c.cfg.MuxRoutes, MinHops: c.cfg.MinHops})
		}
		return appCl.Dial(a)
	}
	nets := []appnet.Type{netType}
	if c.cfg.DmsgFallback {
		nets = append(nets, appnet.TypeDmsg)
	}
	conn, _, err := appnet.DialWithFallback(context.Background(), dial, pk, serverPort, nets...)
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
	if c.appCl == nil {
		return // embedded client (NewClientEmbedded): no app RPC to report to
	}
	if err := c.appCl.SetDetailedStatus(string(status)); err != nil {
		c.logger().WithError(err).Warnf("Failed to set status %v", status)
	}
}

func (c *Client) setConnectionDuration() {
	if c.appCl == nil {
		return
	}
	if err := c.appCl.SetConnectionDuration(c.connectedDuration.Load()); err != nil {
		c.logger().WithError(err).Warn("Failed to set connection duration")
	}
}

func (c *Client) setAppError(appErr error) {
	if c.appCl == nil {
		return
	}
	if err := c.appCl.SetError(appErr.Error()); err != nil {
		c.logger().WithError(err).Warnf("Failed to set error %v", appErr)
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
	c.connectedDuration.Store(0)
	c.setConnectionDuration()
}

func ipFromEnv(key string) (netip.Addr, error) {
	ip, ok, err := IPFromEnv(key)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("error getting IP from %s: %w", key, err)
	}
	if !ok {
		return netip.Addr{}, fmt.Errorf("env arg %s is not provided", key)
	}

	return ip, nil
}

// optionalIPFromEnv resolves a skywire-service address env arg to a direct-route
// IP, but treats an absent arg as "no direct route needed" rather than an error.
// In a dmsg-only deployment these services (dmsg discovery, TP discovery, AR, RF,
// uptime tracker) are reached over dmsg — i.e. through the dmsg servers, which
// are themselves the direct routes — so their own IP is neither configured nor
// required. A dmsg:// address (no IP) likewise yields a nil IP. Callers filter
// out nil IPs. A genuine resolution failure (e.g. bad DNS) is still returned.
func optionalIPFromEnv(key string) (netip.Addr, error) {
	ip, _, err := IPFromEnv(key)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("error getting IP from %s: %w", key, err)
	}
	return ip, nil
}

func filterOutEqualIPs(ips []netip.Addr) []netip.Addr {
	seen := make(map[netip.Addr]bool, len(ips))
	var filtered []netip.Addr
	for _, ip := range ips {
		ip = ip.Unmap()
		if ip.IsValid() && !seen[ip] {
			seen[ip] = true
			filtered = append(filtered, ip)
		}
	}
	return filtered
}

// gateway is the default gateway the direct routes go through.
func (c *Client) gateway() netip.Addr {
	c.directIPSMu.Lock()
	defer c.directIPSMu.Unlock()
	return c.defaultGateway
}

// RepinDirectRoutes moves every direct route onto the current default
// gateway after the visor reports a change of network. Routes through the
// old gateway are removed first, and one the kernel already dropped with
// its interface is not an error. It returns the gateway in use afterwards
// and whether it changed.
func (c *Client) RepinDirectRoutes() (netip.Addr, bool, error) {
	if c.gatewayFn == nil {
		return netip.Addr{}, false, errors.New("no default gateway lookup")
	}
	gw, err := c.gatewayFn()
	if err != nil {
		return netip.Addr{}, false, fmt.Errorf("error getting default network gateway: %w", err)
	}
	if !gw.IsValid() || gw.IsUnspecified() {
		return netip.Addr{}, false, errors.New("no default gateway")
	}
	c.directIPSMu.Lock()
	defer c.directIPSMu.Unlock()
	if gw == c.defaultGateway {
		return gw, false, nil
	}
	for _, ip := range c.directIPs {
		_ = c.delDirect(ip) //nolint:errcheck
	}
	c.defaultGateway = gw
	var errs []error
	for _, ip := range c.directIPs {
		if err := c.addDirect(ip); err != nil {
			errs = append(errs, err)
		}
	}
	return gw, true, errors.Join(errs...)
}
