package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/SkycoinProject/skywire-mainnet/internal/netutil"
	"github.com/SkycoinProject/skywire-mainnet/internal/vpn"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/songgao/water"
)

const (
	ipv4FirstHalfAddr  = "0.0.0.0"
	ipv4SecondHalfAddr = "128.0.0.0"
	ipv4HalfRangeMask  = "128.0.0.0"
)

const (
	appName = "vpn-client"
	netType = appnet.TypeSkynet
	vpnPort = routing.Port(44)
)

var (
	log = app.NewLogger(appName)
	r   = netutil.NewRetrier(time.Second, 0, 1)
)

var serverPKStr = flag.String("srv", "", "PubKey of the server to connect to")

func dialServer(appCl *app.Client, pk cipher.PubKey) (net.Conn, error) {
	var conn net.Conn
	err := r.Do(func() error {
		var err error
		conn, err = appCl.Dial(appnet.Addr{
			Net:    netType,
			PubKey: pk,
			Port:   vpnPort,
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func shakeHands(conn net.Conn, defaultGateway net.IP) (TUNIP, TUNGateway net.IP, err error) {
	unavailableIPs, err := vpn.LocalNetworkInterfaceIPs()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting unavailable private IPs: %w", err)
	}

	unavailableIPs = append(unavailableIPs, defaultGateway)

	cHello := vpn.ClientHello{
		UnavailablePrivateIPs: unavailableIPs,
	}

	log.Debugf("Sending client hello: %v", cHello)

	if err := vpn.WriteJSON(conn, &cHello); err != nil {
		return nil, nil, fmt.Errorf("error sending client hello: %w", err)
	}

	var sHello vpn.ServerHello
	if err := vpn.ReadJSON(conn, &sHello); err != nil {
		return nil, nil, fmt.Errorf("error reading server hello: %w", err)
	}

	log.Debugf("Got server hello: %v", sHello)

	if sHello.Status != vpn.HandshakeStatusOK {
		return nil, nil, fmt.Errorf("got status %d (%s) from the server", sHello.Status, sHello.Status)
	}

	return sHello.TUNIP, sHello.TUNGateway, nil
}

func main() {
	flag.Parse()

	if *serverPKStr == "" {
		log.Fatalln("VPN server pub key is missing")
	}

	serverPK := cipher.PubKey{}
	if err := serverPK.UnmarshalText([]byte(*serverPKStr)); err != nil {
		log.WithError(err).Fatalln("Invalid VPN server pub key")
	}

	log.Infof("Connecting to VPN server %s", serverPK.String())

	defaultGateway, err := vpn.DefaultNetworkGateway()
	if err != nil {
		log.WithError(err).Fatalln("Error getting default network gateway")
	}

	log.Infof("Got default network gateway IP: %s", defaultGateway)

	dmsgDiscIP, ok, err := vpn.IPFromEnv(vpn.DmsgDiscAddrEnvKey)
	if err != nil {
		log.WithError(err).Fatalln("Error getting Dmsg discovery IP")
	}
	if !ok {
		log.Fatalf("Env arg %s is not provided", vpn.DmsgDiscAddrEnvKey)
	}

	dmsgSrvCountStr := os.Getenv(vpn.DmsgAddrsCountEnvKey)
	if dmsgSrvCountStr == "" {
		log.Fatalln("Dmsg servers count is not provided")
	}
	dmsgSrvCount, err := strconv.Atoi(dmsgSrvCountStr)
	if err != nil {
		log.WithError(err).Fatalf("Invalid Dmsg servers count: %s", dmsgSrvCountStr)
	}

	dmsgSrvAddrs := make([]net.IP, 0, dmsgSrvCount)
	for i := 0; i < dmsgSrvCount; i++ {
		dmsgSrvAddr, ok, err := vpn.IPFromEnv(vpn.DmsgAddrEnvPrefix + strconv.Itoa(i))
		if err != nil {
			log.Fatalf("Invalid Dmsg address in key %s", vpn.DmsgAddrEnvPrefix+strconv.Itoa(i))
		}
		if !ok {
			log.Fatalf("Env arg %s is not provided", vpn.DmsgAddrEnvPrefix+strconv.Itoa(i))
		}

		dmsgSrvAddrs = append(dmsgSrvAddrs, dmsgSrvAddr)
	}

	tpDiscIP, ok, err := vpn.IPFromEnv(vpn.TPDiscAddrEnvKey)
	if err != nil {
		log.WithError(err).Fatalln("Error getting transport discovery IP")
	}
	if !ok {
		log.Fatalf("Env arg %s is not provided", vpn.TPDiscAddrEnvKey)
	}

	rfIP, ok, err := vpn.IPFromEnv(vpn.RFAddrEnvKey)
	if err != nil {
		log.WithError(err).Fatalln("Error getting route finder IP")
	}
	if !ok {
		log.Fatalf("Env arg %s is not provided", vpn.RFAddrEnvKey)
	}

	var stcpEntities []net.IP
	stcpTableLenStr := os.Getenv(vpn.STCPTableLenEnvKey)
	if stcpTableLenStr != "" {
		stcpTableLen, err := strconv.Atoi(stcpTableLenStr)
		if err != nil {
			log.WithError(err).Fatalf("Invalid STCP table len: %s", stcpTableLenStr)
		}

		stcpEntities = make([]net.IP, 0, stcpTableLen)
		for i := 0; i < stcpTableLen; i++ {
			stcpKey := os.Getenv(vpn.STCPKeyEnvPrefix + strconv.Itoa(i))
			if stcpKey == "" {
				log.Fatalf("Env arg %s is not provided", vpn.STCPKeyEnvPrefix+strconv.Itoa(i))
			}

			stcpAddr, ok, err := vpn.IPFromEnv(vpn.STCPValueEnvPrefix + stcpKey)
			if err != nil {
				log.WithError(err).
					Fatalf("Error getting IP of STCP item for env key %s", vpn.STCPValueEnvPrefix+stcpKey)
			}
			if !ok {
				log.Fatalf("Env arg %s is not provided", vpn.STCPValueEnvPrefix+stcpKey)
			}

			stcpEntities = append(stcpEntities, stcpAddr)
		}
	}

	var hypervisorAddrs []net.IP
	hypervisorsCountStr := os.Getenv(vpn.HypervisorsCountEnvKey)
	if hypervisorsCountStr != "" {
		hypervisorsCount, err := strconv.Atoi(hypervisorsCountStr)
		if err != nil {
			log.WithError(err).Fatalf("Invalid hypervisors count: %s", hypervisorsCountStr)
		}

		hypervisorAddrs = make([]net.IP, 0, hypervisorsCount)
		for i := 0; i < hypervisorsCount; i++ {
			hypervisorAddr, ok, err := vpn.IPFromEnv(vpn.HypervisorAddrEnvPrefix + strconv.Itoa(i))
			if err != nil {
				log.WithError(err).Fatalf("Error getting IP of hypervisor for env key %s",
					vpn.HypervisorAddrEnvPrefix+strconv.Itoa(i))
			}
			if !ok {
				log.Fatalf("Env arg %s is not provided", vpn.HypervisorAddrEnvPrefix+strconv.Itoa(i))
			}

			hypervisorAddrs = append(hypervisorAddrs, hypervisorAddr)
		}
	}

	appCfg, err := app.ClientConfigFromEnv()
	if err != nil {
		log.WithError(err).Fatalln("Error getting app client config")
	}

	vpnClient, err := app.NewClient(logging.MustGetLogger(fmt.Sprintf("app_%s", appName)), appCfg)
	if err != nil {
		log.WithError(err).Fatalln("Error setting up VPN client")
	}
	defer func() {
		vpnClient.Close()
	}()

	appConn, err := dialServer(vpnClient, serverPK)
	if err != nil {
		log.WithError(err).Fatalln("Error connecting to VPN server")
	}

	log.Infof("Dialed %s", appConn.RemoteAddr())

	tunIP, tunGateway, err := shakeHands(appConn, defaultGateway)
	if err != nil {
		log.WithError(err).Errorln("Error during client/server handshake")
		return
	}

	log.Infof("Performed handshake with %s", appConn.RemoteAddr())
	log.Infof("Local TUN IP: %s", tunIP.String())
	log.Infof("Local TUN gateway: %s", tunGateway.String())

	tun, err := water.New(water.Config{
		DeviceType: water.TUN,
	})
	if err != nil {
		log.WithError(err).Errorln("Error allocating TUN interface")
		return
	}
	defer func() {
		tunName := tun.Name()
		if err := tun.Close(); err != nil {
			log.WithError(err).Errorf("Error closing TUN %s", tunName)
		}
	}()

	log.Infof("Allocated TUN %s", tun.Name())

	osSigs := make(chan os.Signal)

	sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	for _, sig := range sigs {
		signal.Notify(osSigs, sig)
	}

	err = vpn.SetupTUN(tun.Name(), tunIP.String(), vpn.TUNNetmask, tunGateway.String(), vpn.TUNMTU)
	if err != nil {
		log.WithError(err).Errorf("Error setting up TUN %s", tun.Name())
		return
	}

	// route Skywire service traffic through the default gateway
	if !dmsgDiscIP.IsLoopback() {
		log.Infof("Adding direct route to Dmsg discovery: %s", dmsgDiscIP)
		if err := vpn.AddRoute(dmsgDiscIP.String(), defaultGateway.String(), ""); err != nil {
			log.WithError(err).Errorf("Error adding direct route to Dmsg discovery: %s", dmsgDiscIP)
			return
		}
	}
	for _, dmsgIP := range dmsgSrvAddrs {
		if !dmsgIP.IsLoopback() {
			log.Infof("Adding direct route to Dmsg server: %s", dmsgIP)
			if err := vpn.AddRoute(dmsgIP.String(), defaultGateway.String(), ""); err != nil {
				log.WithError(err).Errorf("Error adding direct route to Dmsg server: %s", dmsgIP)
				return
			}
		}
	}
	if !tpDiscIP.IsLoopback() {
		log.Infof("Adding direct route to TP discovery: %s", tpDiscIP)
		if err := vpn.AddRoute(tpDiscIP.String(), defaultGateway.String(), ""); err != nil {
			log.WithError(err).Errorf("Error adding direct route to TP discovery: %s", tpDiscIP)
			return
		}
	}
	if !rfIP.IsLoopback() {
		log.Infof("Adding direct route to RF: %s", rfIP)
		if err := vpn.AddRoute(rfIP.String(), defaultGateway.String(), ""); err != nil {
			log.WithError(err).Errorf("Error adding direct route to RF: %s", rfIP)
			return
		}
	}

	for _, stcpEntity := range stcpEntities {
		if !stcpEntity.IsLoopback() {
			log.Infof("Adding direct STCP route to visor: %s", stcpEntity)
			if err := vpn.AddRoute(stcpEntity.String(), defaultGateway.String(), ""); err != nil {
				log.WithError(err).Errorf("Error adding direct route to visor: %s", stcpEntity)
				return
			}
		}
	}

	for _, hypervisorAddr := range hypervisorAddrs {
		if !hypervisorAddr.IsLoopback() {
			log.Infof("Adding direct route to hypervisor: %s", hypervisorAddr)
			if err := vpn.AddRoute(hypervisorAddr.String(), defaultGateway.String(), ""); err != nil {
				log.WithError(err).Errorf("Error adding direct route to hypervisor: %s", hypervisorAddr)
				return
			}
		}
	}

	log.Infof("Routing all traffic through TUN %s", tun.Name())

	// route all traffic through TUN gateway
	if err := vpn.AddRoute(ipv4FirstHalfAddr, tunGateway.String(), ipv4HalfRangeMask); err != nil {
		log.WithError(err).Errorf("Error routing traffic through TUN %s", tun.Name())
		return
	}
	if err := vpn.AddRoute(ipv4SecondHalfAddr, tunGateway.String(), ipv4HalfRangeMask); err != nil {
		log.WithError(err).Errorf("Error routing traffic through TUN %s", tun.Name())
		return
	}

	defer func() {
		if !dmsgDiscIP.IsLoopback() {
			log.Infof("Removing direct route to Dmsg discovery: %s", dmsgDiscIP)
			if err := vpn.DeleteRoute(dmsgDiscIP.String(), defaultGateway.String(), ""); err != nil {
				log.WithError(err).Errorf("Error removing direct route to Dmsg discovery: %s", dmsgDiscIP)
			}
		}
		for _, dmsgIP := range dmsgSrvAddrs {
			if !dmsgIP.IsLoopback() {
				log.Infof("Removing direct route to Dmsg server: %s", dmsgIP)
				if err := vpn.DeleteRoute(dmsgIP.String(), defaultGateway.String(), ""); err != nil {
					log.WithError(err).Errorf("Error removing direct route to Dmsg server: %s", dmsgIP)
				}
			}
		}
		if !tpDiscIP.IsLoopback() {
			log.Infof("Removing direct route to TP discovery: %s", tpDiscIP)
			if err := vpn.DeleteRoute(tpDiscIP.String(), defaultGateway.String(), ""); err != nil {
				log.WithError(err).Errorf("Error removing direct route to TP discovery: %s", tpDiscIP)
			}
		}
		if !rfIP.IsLoopback() {
			log.Infof("Removing direct route to RF: %s", rfIP)
			if err := vpn.DeleteRoute(rfIP.String(), defaultGateway.String(), ""); err != nil {
				log.WithError(err).Errorf("Error removing direct route to RF: %s", rfIP)
			}
		}

		for _, stcpEntity := range stcpEntities {
			if !stcpEntity.IsLoopback() {
				log.Infof("Removing direct STCP route to visor: %s", stcpEntity)
				if err := vpn.DeleteRoute(stcpEntity.String(), defaultGateway.String(), ""); err != nil {
					log.WithError(err).Errorf("Error removing direct STCP route to visor: %s", stcpEntity)
				}
			}
		}

		for _, hypervisorAddr := range hypervisorAddrs {
			if !hypervisorAddr.IsLoopback() {
				log.Infof("Removing direct route to hypervisor: %s", hypervisorAddr)
				if err := vpn.DeleteRoute(hypervisorAddr.String(), defaultGateway.String(), ""); err != nil {
					log.WithError(err).Errorf("Error removing direct route to hypervisor: %s", hypervisorAddr)
				}
			}
		}

		log.Infoln("Routing all traffic through default network gateway")

		// remove main route
		if err := vpn.DeleteRoute(ipv4FirstHalfAddr, tunGateway.String(), ipv4HalfRangeMask); err != nil {
			log.WithError(err).Errorf("Error routing traffic through default network gateway")
		}
		if err := vpn.DeleteRoute(ipv4SecondHalfAddr, tunGateway.String(), ipv4HalfRangeMask); err != nil {
			log.WithError(err).Errorf("Error routing traffic through default network gateway")
		}
	}()

	// read all system traffic and pass it to the remote VPN server
	go func() {
		if _, err := io.Copy(tun, appConn); err != nil {
			log.WithError(err).Errorf("Error resending traffic from TUN %s to VPN server", tun.Name())
		}
	}()
	go func() {
		if _, err := io.Copy(appConn, tun); err != nil {
			log.WithError(err).Errorf("Error resending traffic from VPN server to TUN %s", tun.Name())
		}
	}()

	<-osSigs
}
