package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"golang.org/x/net/ipv4"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/prometheus/common/log"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/songgao/water"
)

const (
	dmsgDiscAddrEnvKey = "ADDR_DMSG_DISC"
	dmsgAddrEnvKey     = "ADDR_DMSG_SRV"
	tpDiscAddrEnvKey   = "ADDR_TP_DISC"
	rfAddrEnvKey       = "ADDR_RF"

	stcpTableLenEnvKey = "STCP_TABLE_LEN"
	stcpKeyEnvPrefix   = "STCP_TABLE_KEY_"
	stcpValueEnvPrefix = "STCP_TABLE_"

	hypervisorsCountEnvKey  = "HYPERVISOR_COUNT"
	hypervisorAddrEnvPrefix = "ADDR_HYPERVISOR_"
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

type RouteArg struct {
	IP      string
	Gateway string
	Netmask string
}

func run(bin string, args ...string) {
	//cmd := exec.Command("sh -c \"ip " + strings.Join(args, " ") + "\"")
	cmd := exec.Command(bin, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if nil != err {
		log.Fatalf("Error running %s: %v\n", bin, err)
	}
}

func setupTUN(ifcName, ip, netmask, gateway string, mtu int) {
	run("/sbin/ifconfig", ifcName, ip, gateway, "mtu", strconv.Itoa(mtu), "netmask", netmask, "up")
}

func addRoute(ip, gateway, netmask string) {
	if netmask == "" {
		run("/sbin/route", "add", "-net", ip, gateway)
	} else {
		run("/sbin/route", "add", "-net", ip, gateway, netmask)
	}
}

func deleteRoute(ip, gateway, netmask string) {
	if netmask == "" {
		run("/sbin/route", "delete", "-net", ip, gateway)
	} else {
		run("/sbin/route", "delete", "-net", ip, gateway, netmask)
	}
}

func getDefaultGatewayIP() (net.IP, error) {
	cmd := "netstat -rn | grep default | grep en | awk '{print $2}'"
	outBytes, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return nil, fmt.Errorf("error running command: %w", err)
	}

	outBytes = bytes.TrimRight(outBytes, "\n")

	outLines := bytes.Split(outBytes, []byte{'\n'})

	for _, l := range outLines {
		if bytes.Count(l, []byte{'.'}) != 3 {
			// initially look for IPv4 address
			continue
		}

		ip := net.ParseIP(string(l))
		if ip != nil {
			return ip, nil
		}
	}

	return nil, errors.New("couldn't find default gateway IP")
}

var (
//log = app.NewLogger(appName)
)

func main() {
	/*var serverPKStr = flag.String("srv", "", "PubKey of the server to connect to")
	if *serverPKStr == "" {
		log.Fatalln("VPN server pub key is missing")
	}

	serverPK := cipher.PubKey{}
	if err := serverPK.UnmarshalText([]byte(*serverPKStr)); err != nil {
		log.Fatalf("Invalid VPN server pub key: %v", err)
	}

	defaultGatewayIP, err := getDefaultGatewayIP()
	if err != nil {
		log.Fatalf("Error getting default network gateway: %v", err)
	}

	dmsgDiscIP, ok, err := ipFromEnv(dmsgDiscAddrEnvKey)
	if err != nil {
		log.Fatalf("Error getting Dmsg discovery IP: %v", err)
	}
	if !ok {
		log.Fatalf("Env arg %s is not provided", dmsgDiscAddrEnvKey)
	}

	dmsgIP, ok, err := ipFromEnv(dmsgAddrEnvKey)
	if err != nil {
		log.Fatalf("Error getting Dmsg IP: %v", err)
	}
	if !ok {
		log.Fatalf("Env arg %s is not provided", dmsgAddrEnvKey)
	}

	tpDiscIP, ok, err := ipFromEnv(tpDiscAddrEnvKey)
	if err != nil {
		log.Fatalf("Error getting transport discovery IP: %v", err)
	}
	if !ok {
		log.Fatalf("Env arg %s is not provided", tpDiscAddrEnvKey)
	}

	rfIP, ok, err := ipFromEnv(rfAddrEnvKey)
	if err != nil {
		log.Fatalf("Error getting route finder IP: %v", err)
	}
	if !ok {
		log.Fatalf("Env arg %s is not provided", rfAddrEnvKey)
	}

	var stcpEntities []net.IP
	stcpTableLenStr := os.Getenv(stcpTableLenEnvKey)
	if stcpTableLenStr != "" {
		stcpTableLen, err := strconv.Atoi(stcpTableLenStr)
		if err != nil {
			log.Fatalf("Invalid STCP table len: %v", err)
		}

		stcpEntities = make([]net.IP, 0, stcpTableLen)
		for i := 0; i < stcpTableLen; i++ {
			stcpKey := os.Getenv(stcpKeyEnvPrefix + strconv.Itoa(i))
			if stcpKey == "" {
				log.Fatalf("Env arg %s is not provided", stcpKeyEnvPrefix+strconv.Itoa(i))
			}

			stcpAddrStr := os.Getenv(stcpValueEnvPrefix + stcpKey)
			if stcpAddrStr == "" {
				log.Fatalf("Env arg %s is not provided", stcpValueEnvPrefix+stcpKey)
			}

			stcpAddr := net.ParseIP(stcpAddrStr)
			if stcpAddr == nil {
				log.Fatalf("Invalid STCP address in key %s: %v", stcpValueEnvPrefix+stcpKey, err)
			}

			stcpEntities = append(stcpEntities, stcpAddr)
		}
	}

	var hypervisorAddrs []net.IP
	hypervisorsCountStr := os.Getenv(hypervisorsCountEnvKey)
	if hypervisorsCountStr != "" {
		hypervisorsCount, err := strconv.Atoi(hypervisorsCountStr)
		if err != nil {
			log.Fatalf("Invalid hypervisors count: %v", err)
		}

		hypervisorAddrs = make([]net.IP, 0, hypervisorsCount)
		for i := 0; i < hypervisorsCount; i++ {
			hypervisorAddrStr := os.Getenv(hypervisorAddrEnvPrefix + strconv.Itoa(i))
			if hypervisorAddrStr == "" {
				log.Fatalf("Env arg %s is missing", hypervisorAddrEnvPrefix+strconv.Itoa(i))
			}

			hypervisorAddr := net.ParseIP(hypervisorAddrStr)
			if hypervisorAddr == nil {
				log.Fatalf("Invalid hypervisor address in key %s: %v", hypervisorAddrEnvPrefix+strconv.Itoa(i), err)
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
	}*/

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

	setupTUN(ifc.Name(), tunIP, tunNetmask, tunGateway, tunMTU)

	// route Skywire service traffic through the default gateway
	/*if !dmsgDiscIP.IsLoopback() {
		addRoute(dmsgDiscIP.String(), defaultGatewayIP.String(), "")
	}
	if !dmsgIP.IsLoopback() {
		addRoute(dmsgIP.String(), defaultGatewayIP.String(), "")
	}
	if !tpDiscIP.IsLoopback() {
		addRoute(tpDiscIP.String(), defaultGatewayIP.String(), "")
	}
	if !rfIP.IsLoopback() {
		addRoute(rfIP.String(), defaultGatewayIP.String(), "")
	}

	for _, stcpEntity := range stcpEntities {
		if !stcpEntity.IsLoopback() {
			addRoute(stcpEntity.String(), defaultGatewayIP.String(), "")
		}
	}

	for _, hypervisorAddr := range hypervisorAddrs {
		if !hypervisorAddr.IsLoopback() {
			addRoute(hypervisorAddr.String(), defaultGatewayIP.String(), "")
		}
	}

	defer func() {
		if !dmsgDiscIP.IsLoopback() {
			deleteRoute(dmsgDiscIP.String(), defaultGatewayIP.String(), "")
		}
		if !dmsgIP.IsLoopback() {
			deleteRoute(dmsgIP.String(), defaultGatewayIP.String(), "")
		}
		if !tpDiscIP.IsLoopback() {
			deleteRoute(tpDiscIP.String(), defaultGatewayIP.String(), "")
		}
		if !rfIP.IsLoopback() {
			deleteRoute(rfIP.String(), defaultGatewayIP.String(), "")
		}

		for _, stcpEntity := range stcpEntities {
			if !stcpEntity.IsLoopback() {
				deleteRoute(stcpEntity.String(), defaultGatewayIP.String(), "")
			}
		}

		for _, hypervisorAddr := range hypervisorAddrs {
			if !hypervisorAddr.IsLoopback() {
				deleteRoute(hypervisorAddr.String(), defaultGatewayIP.String(), "")
			}
		}

		// remove main route
		deleteRoute(ipv4FirstHalfAddr, tunGateway, ipv4HalfRangeMask)
		deleteRoute(ipv4SecondHalfAddr, tunGateway, ipv4HalfRangeMask)
	}()

	// route all traffic through TUN gateway
	addRoute(ipv4FirstHalfAddr, tunGateway, ipv4HalfRangeMask)
	addRoute(ipv4SecondHalfAddr, tunGateway, ipv4HalfRangeMask)

	// read all system traffic and pass it to the remote VPN server
	go func() {
		if err := copyTraffic(ifc, appConn); err != nil {
			log.Fatalf("Error resending traffic from TUN %s to VPN server: %v", ifc.Name(), err)
		}
	}()
	go func() {
		if err := copyTraffic(appConn, ifc); err != nil {
			log.Fatalf("Error resending traffic from VPN server to TUN %s: %v", ifc.Name(), err)
		}
	}()*/

	/*conn, err := net.Dial("tcp", "192.168.1.18:2000")
	if err != nil {
		panic(err)
	}*/

	lis, err := net.Listen("tcp", ":2000")
	if err != nil {
		panic(err)
	}

	conn, err := lis.Accept()
	if err != nil {
		panic(err)
	}

	go func() {
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
	}()

	<-shutdownC

	log.Fatalln("DONE")
}

func copyTraffic(from, to io.ReadWriteCloser) error {
	buf := make([]byte, bufSize)
	for {
		rn, rerr := from.Read(buf)
		if rerr != nil {
			return fmt.Errorf("error reading from RWC: %v", rerr)
		}

		header, err := ipv4.ParseHeader(buf[:rn])
		if err != nil {
			log.Errorf("Error parsing IP header, skipping...")
			continue
		}

		// TODO: match IPs?
		log.Infof("Sending IP packet %v->%v", header.Src, header.Dst)

		totalWritten := 0
		for totalWritten != rn {
			wn, werr := to.Write(buf[:rn])
			if werr != nil {
				return fmt.Errorf("error writing to RWC: %v", err)
			}

			totalWritten += wn
		}
	}
}

func ipFromEnv(key string) (net.IP, bool, error) {
	addr := os.Getenv(key)
	if addr == "" {
		return nil, false, nil
	}

	ip := net.ParseIP(addr)
	if ip != nil {
		return ip, true, nil
	}

	ips, err := net.LookupIP(addr)
	if err != nil {
		return nil, false, err
	}
	if len(ips) == 0 {
		return nil, false, fmt.Errorf("couldn't resolve IPs of %s", addr)
	}

	// initially take just the first one
	ip = ips[0]

	return ip, true, nil
}
