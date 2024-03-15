// Package vpn internal/vpn/client.go
package vpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/util/osutil"
)

// Client is a VPN lite client.
type Client struct {
	log       *logrus.Logger
	cfg       ClientConfig
	appCl     *app.Client
	closeC    chan struct{}
	closeOnce sync.Once

	tunMu      sync.Mutex
	tun        TUNDevice
	tunCreated bool
}

// NewLiteClient creates VPN lite client instance.
func NewLiteClient(cfg ClientConfig, appCl *app.Client) (*Client, error) {

	log := logrus.New()

	return &Client{
		log:    log,
		cfg:    cfg,
		appCl:  appCl,
		closeC: make(chan struct{}),
	}, nil
}

// Serve dials VPN server, sets up TUN and establishes VPN session.
func (c *Client) Serve() error {

	c.setAppStatus(appserver.AppDetailedStatusStarting)

	// we call this preliminary, so it will be called on app stop
	defer func() {

		if err := c.closeTUN(); err != nil {
			fmt.Printf("Failed to close TUN: %v\n", err)
		}
		fmt.Println("Closed TUN")
	}()

	defer func() {
		c.setAppStatus(appserver.AppDetailedStatusShuttingDown)
	}()

	c.setAppStatus(appserver.AppDetailedStatusVPNConnecting)

	r := netutil.NewRetrier(nil, netutil.DefaultInitBackoff, netutil.DefaultMaxBackoff, 3, netutil.DefaultFactor).
		WithErrWhitelist(errHandshakeStatusForbidden, errHandshakeStatusInternalError, errHandshakeNoFreeIPs,
			errHandshakeStatusBadRequest, errNoTransportFound, errTransportNotFound, ErrSetupNode, ErrNotPermitted,
			ErrServerOffline)

	err := r.Do(context.Background(), func() error {
		if c.isClosed() {
			return nil
		}

		if err := c.dialServeConn(); err != nil {
			switch err {
			case errHandshakeStatusForbidden, errHandshakeStatusInternalError, errHandshakeNoFreeIPs,
				errHandshakeStatusBadRequest, errNoTransportFound, errTransportNotFound, ErrSetupNode, ErrNotPermitted,
				ErrServerOffline:
				c.setAppError(err)
				return err
			default:
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

// Close closes client.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.closeC)
	})
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

	return c.tun.Close()
}

func (c *Client) setupTUN(tunIP, tunGateway net.IP) error {
	c.tunMu.Lock()
	defer c.tunMu.Unlock()

	if !c.tunCreated {
		return errors.New("TUN is not created")
	}

	return SetupTUN(c.tun.Name(), tunIP.String()+TUNNetmaskCIDR, tunGateway.String(), TUNMTU)
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

	fmt.Printf("TUN %s all sets\n", tunIP)

	c.setAppStatus(appserver.AppDetailedStatusRunning)

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
		fmt.Printf("error serving app conn: %s\n", err)
		return err
	}

	return nil
}

func (c *Client) shakeHands(conn net.Conn) (TUNIP, TUNGateway net.IP, err error) {
	unavailableIPs, err := netutil.LocalNetworkInterfaceIPs()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting unavailable private IPs: %w", err)
	}

	cHello := ClientHello{
		UnavailablePrivateIPs: unavailableIPs,
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

	fmt.Printf("Got server hello: %v\n", sHello)

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

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func SetupTUN(ifcName, ipCIDR, _ string, mtu int) error {
	if err := osutil.Run("ip", "a", "add", ipCIDR, "dev", ifcName); err != nil {
		return fmt.Errorf("error assigning IP: %w", err)
	}

	if err := osutil.Run("ip", "link", "set", "dev", ifcName, "mtu", strconv.Itoa(mtu)); err != nil {
		return fmt.Errorf("error setting MTU: %w", err)
	}

	if err := osutil.Run("ip", "link", "set", ifcName, "up"); err != nil {
		return fmt.Errorf("error setting interface up: %w", err)
	}

	return nil
}
