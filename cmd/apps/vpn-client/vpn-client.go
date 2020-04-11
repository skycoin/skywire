package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

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
	tunIP      = "192.168.255.6"
	tunNetmask = "255.255.255.255"
	tunGateway = "192.168.255.5"
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

const (
	bufSize = 1800
)

var (
	log = app.NewLogger(appName)
)

func main() {
	var serverPKStr = flag.String("srv", "", "PubKey of the server to connect to")
	if *serverPKStr == "" {
		*serverPKStr = "037e81486e82c62e449a8d0ebdf7b87fafd92e2d676863703d2a295652770f141b"
		// TODO: fix this
		//log.Fatalln("VPN server pub key is missing")
	}

	serverPK := cipher.PubKey{}
	if err := serverPK.UnmarshalText([]byte(*serverPKStr)); err != nil {
		log.WithError(err).Fatalln("Invalid VPN server pub key")
	}

	log.Infof("Connecting to VPN server %s", serverPK.String())

	defaultGatewayIP, err := vpn.DefaultGatewayIP()
	if err != nil {
		log.WithError(err).Fatalln("Error getting default network gateway")
	}

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
		log.Fatalf("Error getting app client config: %v", err)
	}

	vpnClient, err := app.NewClient(logging.MustGetLogger(fmt.Sprintf("app_%s", appName)), appCfg)
	if err != nil {
		log.Fatalf("Error setting up VPN client: v", err)
	}
	defer func() {
		vpnClient.Close()
	}()

	appConn, err := vpnClient.Dial(appnet.Addr{
		Net:    netType,
		PubKey: serverPK,
		Port:   vpnPort,
	})
	if err != nil {
		log.Fatalf("Error connecting to VPN server: %v", err)
	}

	log.Infof("Dialed to %s", appConn.RemoteAddr())

	ifc, err := water.New(water.Config{
		DeviceType: water.TUN,
	})
	if nil != err {
		log.Fatalf("Error allocating TUN interface: %v", err)
	}
	defer func() {
		tunName := ifc.Name()
		if err := ifc.Close(); err != nil {
			log.Errorf("Error closing TUN %s: %v", tunName, err)
		}
	}()

	log.Infof("Allocated TUN %s", ifc.Name())

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

	vpn.SetupTUN(ifc.Name(), tunIP, tunNetmask, tunGateway, tunMTU)

	// route Skywire service traffic through the default gateway
	if !dmsgDiscIP.IsLoopback() {
		log.Infof("Adding direct route to Dmsg discovery: %s", dmsgDiscIP)
		vpn.AddRoute(dmsgDiscIP.String(), defaultGatewayIP.String(), "")
	}
	for _, dmsgIP := range dmsgSrvAddrs {
		if !dmsgIP.IsLoopback() {
			log.Infof("Adding direct route to Dmsg server: %s", dmsgIP)
			vpn.AddRoute(dmsgIP.String(), defaultGatewayIP.String(), "")
		}
	}
	if !tpDiscIP.IsLoopback() {
		log.Infof("Adding direct route to TP discovery: %s", tpDiscIP)
		vpn.AddRoute(tpDiscIP.String(), defaultGatewayIP.String(), "")
	}
	if !rfIP.IsLoopback() {
		log.Infof("Adding direct route to RF: %s", rfIP)
		vpn.AddRoute(rfIP.String(), defaultGatewayIP.String(), "")
	}

	for _, stcpEntity := range stcpEntities {
		if !stcpEntity.IsLoopback() {
			log.Infof("Adding direct STCP route to node: %s", stcpEntity)
			vpn.AddRoute(stcpEntity.String(), defaultGatewayIP.String(), "")
		}
	}

	for _, hypervisorAddr := range hypervisorAddrs {
		if !hypervisorAddr.IsLoopback() {
			log.Infof("Adding direct route to hypervisor: %s", hypervisorAddr)
			vpn.AddRoute(hypervisorAddr.String(), defaultGatewayIP.String(), "")
		}
	}

	log.Infof("Routing all traffic through TUN %s", ifc.Name())

	// route all traffic through TUN gateway
	vpn.AddRoute(ipv4FirstHalfAddr, tunGateway, ipv4HalfRangeMask)
	vpn.AddRoute(ipv4SecondHalfAddr, tunGateway, ipv4HalfRangeMask)

	defer func() {
		if !dmsgDiscIP.IsLoopback() {
			log.Infof("Removing direct route to Dmsg discovery: %s", dmsgDiscIP)
			vpn.DeleteRoute(dmsgDiscIP.String(), defaultGatewayIP.String(), "")
		}
		for _, dmsgIP := range dmsgSrvAddrs {
			if !dmsgIP.IsLoopback() {
				log.Infof("Removing direct route to Dmsg server: %s", dmsgIP)
				vpn.DeleteRoute(dmsgIP.String(), defaultGatewayIP.String(), "")
			}
		}
		if !tpDiscIP.IsLoopback() {
			log.Infof("Removing direct route to TP discovery: %s", tpDiscIP)
			vpn.DeleteRoute(tpDiscIP.String(), defaultGatewayIP.String(), "")
		}
		if !rfIP.IsLoopback() {
			log.Infof("Removing direct route to RG: %s", rfIP)
			vpn.DeleteRoute(rfIP.String(), defaultGatewayIP.String(), "")
		}

		for _, stcpEntity := range stcpEntities {
			if !stcpEntity.IsLoopback() {
				log.Infof("Removing direct STCP route to node: %s", stcpEntity)
				vpn.DeleteRoute(stcpEntity.String(), defaultGatewayIP.String(), "")
			}
		}

		for _, hypervisorAddr := range hypervisorAddrs {
			if !hypervisorAddr.IsLoopback() {
				log.Infof("Removing direct route to hypervisor: %s", hypervisorAddr)
				vpn.DeleteRoute(hypervisorAddr.String(), defaultGatewayIP.String(), "")
			}
		}

		log.Infoln("Routing all traffic through default network gateway")

		// remove main route
		vpn.DeleteRoute(ipv4FirstHalfAddr, tunGateway, ipv4HalfRangeMask)
		vpn.DeleteRoute(ipv4SecondHalfAddr, tunGateway, ipv4HalfRangeMask)
	}()

	// read all system traffic and pass it to the remote VPN server
	go func() {
		if err := vpn.CopyTraffic(ifc, appConn); err != nil {
			log.Fatalf("Error resending traffic from TUN %s to VPN server: %v", ifc.Name(), err)
		}
	}()
	go func() {
		if err := vpn.CopyTraffic(appConn, ifc); err != nil {
			log.Fatalf("Error resending traffic from VPN server to TUN %s: %v", ifc.Name(), err)
		}
	}()

	// TODO: keep for a while for testing purposes
	/*conn, err := net.Dial("tcp", "192.168.1.18:2000")
	if err != nil {
		panic(err)
	}*/

	/*lis, err := net.Listen("tcp", ":2000")
	if err != nil {
		panic(err)
	}

	conn, err := lis.Accept()
	if err != nil {
		panic(err)
	}*/

	/*go func() {
		buf := make([]byte, bufSize)
		for {
			rn, rerr := ifc.Read(buf)
			if rerr != nil {
				panic(fmt.Errorf("error reading from RWC: %v", rerr))
			}

			header, err := ipv4.ParseHeader(buf[:rn])
			if err != nil {
				log.Errorf("Error parsing IP header, skipping...")
				continue
			}

			if !header.Dst.Equal(net.IPv4(64, 233, 161, 101)) {
				continue
			}

			// TODO: match IPs?
			log.Infof("Sending  OUTgoing IP packet %v->%v", header.Src, header.Dst)

			totalWritten := 0
			for totalWritten != rn {
				wn, werr := conn.Write(buf[:rn])
				if werr != nil {
					panic(fmt.Errorf("error writing to RWC: %v", err))
				}

				totalWritten += wn
			}
		}
	}()

	go func() {
		buf := make([]byte, bufSize)
		for {
			rn, rerr := conn.Read(buf)
			if rerr != nil {
				panic(fmt.Errorf("error reading from RWC: %v", rerr))
			}

			header, err := ipv4.ParseHeader(buf[:rn])
			if err != nil {
				log.Errorf("Error parsing IP header, skipping...")
				continue
			}

			// TODO: match IPs?
			log.Infof("Sending INcoming IP packet %v->%v", header.Src, header.Dst)

			totalWritten := 0
			for totalWritten != rn {
				wn, werr := ifc.Write(buf[:rn])
				if werr != nil {
					panic(fmt.Errorf("error writing to RWC: %v", err))
				}

				totalWritten += wn
			}
		}
	}()*/

	<-shutdownC

	log.Fatalln("DONE")
}
