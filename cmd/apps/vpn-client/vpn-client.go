package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/SkycoinProject/skywire-mainnet/internal/netutil"
	"github.com/SkycoinProject/skywire-mainnet/internal/vpn/vpnenv"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/SkycoinProject/skywire-mainnet/internal/vpn"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/songgao/water"
)

const (
	tunIP      = "192.168.255.4"
	tunNetmask = "255.255.255.248"
	tunGateway = "192.168.255.3"
	tunMTU     = 1500
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

	unavailableIPs, err := vpn.GetIPsToReserve()
	if err != nil {
		log.WithError(err).Fatalln("Error getting unavailable private IPs")
	}

	defaultGatewayIP, err := vpn.DefaultGatewayIP()
	if err != nil {
		log.WithError(err).Fatalln("Error getting default network gateway")
	}

	unavailableIPs = append(unavailableIPs, defaultGatewayIP)

	log.Infof("Got default network gateway IP: %s", defaultGatewayIP)

	dmsgDiscIP, ok, err := vpn.IPFromEnv(vpnenv.DmsgDiscAddrEnvKey)
	if err != nil {
		log.WithError(err).Fatalln("Error getting Dmsg discovery IP")
	}
	if !ok {
		log.Fatalf("Env arg %s is not provided", vpnenv.DmsgDiscAddrEnvKey)
	}

	dmsgSrvCountStr := os.Getenv(vpnenv.DmsgAddrsCountEnvKey)
	if dmsgSrvCountStr == "" {
		log.Fatalln("Dmsg servers count is not provided")
	}
	dmsgSrvCount, err := strconv.Atoi(dmsgSrvCountStr)
	if err != nil {
		log.WithError(err).Fatalf("Invalid Dmsg servers count: %s", dmsgSrvCountStr)
	}

	dmsgSrvAddrs := make([]net.IP, 0, dmsgSrvCount)
	for i := 0; i < dmsgSrvCount; i++ {
		dmsgSrvAddr, ok, err := vpn.IPFromEnv(vpnenv.DmsgAddrEnvPrefix + strconv.Itoa(i))
		if err != nil {
			log.Fatalf("Invalid Dmsg address in key %s", vpnenv.DmsgAddrEnvPrefix+strconv.Itoa(i))
		}
		if !ok {
			log.Fatalf("Env arg %s is not provided", vpnenv.DmsgAddrEnvPrefix+strconv.Itoa(i))
		}

		dmsgSrvAddrs = append(dmsgSrvAddrs, dmsgSrvAddr)
	}

	tpDiscIP, ok, err := vpn.IPFromEnv(vpnenv.TPDiscAddrEnvKey)
	if err != nil {
		log.WithError(err).Fatalln("Error getting transport discovery IP")
	}
	if !ok {
		log.Fatalf("Env arg %s is not provided", vpnenv.TPDiscAddrEnvKey)
	}

	rfIP, ok, err := vpn.IPFromEnv(vpnenv.RFAddrEnvKey)
	if err != nil {
		log.WithError(err).Fatalln("Error getting route finder IP")
	}
	if !ok {
		log.Fatalf("Env arg %s is not provided", vpnenv.RFAddrEnvKey)
	}

	var stcpEntities []net.IP
	stcpTableLenStr := os.Getenv(vpnenv.STCPTableLenEnvKey)
	if stcpTableLenStr != "" {
		stcpTableLen, err := strconv.Atoi(stcpTableLenStr)
		if err != nil {
			log.WithError(err).Fatalf("Invalid STCP table len: %s", stcpTableLenStr)
		}

		stcpEntities = make([]net.IP, 0, stcpTableLen)
		for i := 0; i < stcpTableLen; i++ {
			stcpKey := os.Getenv(vpnenv.STCPKeyEnvPrefix + strconv.Itoa(i))
			if stcpKey == "" {
				log.Fatalf("Env arg %s is not provided", vpnenv.STCPKeyEnvPrefix+strconv.Itoa(i))
			}

			stcpAddr, ok, err := vpn.IPFromEnv(vpnenv.STCPValueEnvPrefix + stcpKey)
			if err != nil {
				log.WithError(err).
					Fatalf("Error getting IP of STCP item for env key %s", vpnenv.STCPValueEnvPrefix+stcpKey)
			}
			if !ok {
				log.Fatalf("Env arg %s is not provided", vpnenv.STCPValueEnvPrefix+stcpKey)
			}

			stcpEntities = append(stcpEntities, stcpAddr)
		}
	}

	var hypervisorAddrs []net.IP
	hypervisorsCountStr := os.Getenv(vpnenv.HypervisorsCountEnvKey)
	if hypervisorsCountStr != "" {
		hypervisorsCount, err := strconv.Atoi(hypervisorsCountStr)
		if err != nil {
			log.WithError(err).Fatalf("Invalid hypervisors count: %s", hypervisorsCountStr)
		}

		hypervisorAddrs = make([]net.IP, 0, hypervisorsCount)
		for i := 0; i < hypervisorsCount; i++ {
			hypervisorAddr, ok, err := vpn.IPFromEnv(vpnenv.HypervisorAddrEnvPrefix + strconv.Itoa(i))
			if err != nil {
				log.WithError(err).Fatalf("Error getting IP of hypervisor for env key %s",
					vpnenv.HypervisorAddrEnvPrefix+strconv.Itoa(i))
			}
			if !ok {
				log.Fatalf("Env arg %s is not provided", vpnenv.HypervisorAddrEnvPrefix+strconv.Itoa(i))
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

	clHello := vpn.ClientHello{
		UnavailablePrivateIPs: unavailableIPs,
	}

	clHelloBytes, err := json.Marshal(&clHello)
	if err != nil {
		log.WithError(err).Errorln("Error marshaling client hello")
		return
	}

	if _, err := appConn.Write(clHelloBytes); err != nil {
		log.WithError(err).Errorln("Error sending client hello")
		return
	}

	var sHelloBytes []byte
	buf := make([]byte, 1024)
	for {
		n, err := appConn.Read(buf)
		if err != nil {
			log.WithError(err).Errorln("Error reading server hello")
			return
		}

		sHelloBytes = append(sHelloBytes, buf[:n]...)

		if n < 1024 {
			break
		}
	}

	var sHello vpn.ServerHello
	if err := json.Unmarshal(sHelloBytes, &sHello); err != nil {
		log.WithError(err).Errorln("Error unmarshaling server hello")
		return
	}

	if sHello.Status != vpn.NegotiationStatusOK {
		log.Errorf("Got status %v from the server", sHello.Status)
		return
	}

	log.Infof("Dialed %s", appConn.RemoteAddr())

	tun, err := water.New(water.Config{
		DeviceType: water.TUN,
	})
	if nil != err {
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

	shutdownC := make(chan struct{})

	go func() {
		<-osSigs

		shutdownC <- struct{}{}
	}()

	if err := vpn.SetupTUN(tun.Name(), sHello.TUNIP.String(), tunNetmask, sHello.TUNGateway.String(), tunMTU); err != nil {
		log.WithError(err).Errorf("Error setting up TUN %s", tun.Name())
		return
	}

	// route Skywire service traffic through the default gateway
	if !dmsgDiscIP.IsLoopback() {
		log.Infof("Adding direct route to Dmsg discovery: %s", dmsgDiscIP)
		if err := vpn.AddRoute(dmsgDiscIP.String(), defaultGatewayIP.String(), ""); err != nil {
			log.WithError(err).Errorf("Error adding direct route to Dmsg discovery: %s", dmsgDiscIP)
			return
		}
	}
	for _, dmsgIP := range dmsgSrvAddrs {
		if !dmsgIP.IsLoopback() {
			log.Infof("Adding direct route to Dmsg server: %s", dmsgIP)
			if err := vpn.AddRoute(dmsgIP.String(), defaultGatewayIP.String(), ""); err != nil {
				log.WithError(err).Errorf("Error adding direct route to Dmsg server: %s", dmsgIP)
				return
			}
		}
	}
	if !tpDiscIP.IsLoopback() {
		log.Infof("Adding direct route to TP discovery: %s", tpDiscIP)
		if err := vpn.AddRoute(tpDiscIP.String(), defaultGatewayIP.String(), ""); err != nil {
			log.WithError(err).Errorf("Error adding direct route to TP discovery: %s", tpDiscIP)
			return
		}
	}
	if !rfIP.IsLoopback() {
		log.Infof("Adding direct route to RF: %s", rfIP)
		if err := vpn.AddRoute(rfIP.String(), defaultGatewayIP.String(), ""); err != nil {
			log.WithError(err).Errorf("Error adding direct route to RF: %s", rfIP)
			return
		}
	}

	for _, stcpEntity := range stcpEntities {
		if !stcpEntity.IsLoopback() {
			log.Infof("Adding direct STCP route to visor: %s", stcpEntity)
			if err := vpn.AddRoute(stcpEntity.String(), defaultGatewayIP.String(), ""); err != nil {
				log.WithError(err).Errorf("Error adding direct route to visor: %s", stcpEntity)
				return
			}
		}
	}

	for _, hypervisorAddr := range hypervisorAddrs {
		if !hypervisorAddr.IsLoopback() {
			log.Infof("Adding direct route to hypervisor: %s", hypervisorAddr)
			if err := vpn.AddRoute(hypervisorAddr.String(), defaultGatewayIP.String(), ""); err != nil {
				log.WithError(err).Errorf("Error adding direct route to hypervisor: %s", hypervisorAddr)
				return
			}
		}
	}

	log.Infof("Routing all traffic through TUN %s", tun.Name())

	// route all traffic through TUN gateway
	if err := vpn.AddRoute(ipv4FirstHalfAddr, tunGateway, ipv4HalfRangeMask); err != nil {
		log.WithError(err).Errorf("Error routing traffic through TUN %s", tun.Name())
		return
	}
	if err := vpn.AddRoute(ipv4SecondHalfAddr, tunGateway, ipv4HalfRangeMask); err != nil {
		log.WithError(err).Errorf("Error routing traffic through TUN %s", tun.Name())
		return
	}

	defer func() {
		if !dmsgDiscIP.IsLoopback() {
			log.Infof("Removing direct route to Dmsg discovery: %s", dmsgDiscIP)
			if err := vpn.DeleteRoute(dmsgDiscIP.String(), defaultGatewayIP.String(), ""); err != nil {
				log.WithError(err).Errorf("Error removing direct route to Dmsg discovery: %s", dmsgDiscIP)
			}
		}
		for _, dmsgIP := range dmsgSrvAddrs {
			if !dmsgIP.IsLoopback() {
				log.Infof("Removing direct route to Dmsg server: %s", dmsgIP)
				if err := vpn.DeleteRoute(dmsgIP.String(), defaultGatewayIP.String(), ""); err != nil {
					log.WithError(err).Errorf("Error removing direct route to Dmsg server: %s", dmsgIP)
				}
			}
		}
		if !tpDiscIP.IsLoopback() {
			log.Infof("Removing direct route to TP discovery: %s", tpDiscIP)
			if err := vpn.DeleteRoute(tpDiscIP.String(), defaultGatewayIP.String(), ""); err != nil {
				log.WithError(err).Errorf("Error removing direct route to TP discovery: %s", tpDiscIP)
			}
		}
		if !rfIP.IsLoopback() {
			log.Infof("Removing direct route to RF: %s", rfIP)
			if err := vpn.DeleteRoute(rfIP.String(), defaultGatewayIP.String(), ""); err != nil {
				log.WithError(err).Errorf("Error removing direct route to RF: %s", rfIP)
			}
		}

		for _, stcpEntity := range stcpEntities {
			if !stcpEntity.IsLoopback() {
				log.Infof("Removing direct STCP route to visor: %s", stcpEntity)
				if err := vpn.DeleteRoute(stcpEntity.String(), defaultGatewayIP.String(), ""); err != nil {
					log.WithError(err).Errorf("Error removing direct STCP route to visor: %s", stcpEntity)
				}
			}
		}

		for _, hypervisorAddr := range hypervisorAddrs {
			if !hypervisorAddr.IsLoopback() {
				log.Infof("Removing direct route to hypervisor: %s", hypervisorAddr)
				if err := vpn.DeleteRoute(hypervisorAddr.String(), defaultGatewayIP.String(), ""); err != nil {
					log.WithError(err).Errorf("Error removing direct route to hypervisor: %s", hypervisorAddr)
				}
			}
		}

		log.Infoln("Routing all traffic through default network gateway")

		// remove main route
		if err := vpn.DeleteRoute(ipv4FirstHalfAddr, tunGateway, ipv4HalfRangeMask); err != nil {
			log.WithError(err).Errorf("Error routing traffic through default network gateway")
		}
		if err := vpn.DeleteRoute(ipv4SecondHalfAddr, tunGateway, ipv4HalfRangeMask); err != nil {
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

	<-shutdownC
}
