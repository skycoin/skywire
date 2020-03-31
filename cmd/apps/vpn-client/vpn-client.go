package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
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

func main() {
	defaultGatewayIP, err := getDefaultGatewayIP()
	if err != nil {
		panic(err)
	}

	dmsgDiscIP, ok, err := ipFromEnv(dmsgDiscAddrEnvKey)
	if err != nil {
		panic(err)
	}
	if !ok {
		panic(fmt.Errorf("%s value is not provided", dmsgDiscAddrEnvKey))
	}

	dmsgIP, ok, err := ipFromEnv(dmsgAddrEnvKey)
	if err != nil {
		panic(err)
	}
	if !ok {
		panic(fmt.Errorf("%s value is not provided", dmsgAddrEnvKey))
	}

	tpDiscIP, ok, err := ipFromEnv(tpDiscAddrEnvKey)
	if err != nil {
		panic(err)
	}
	if !ok {
		panic(fmt.Errorf("%s value is not provided", tpDiscAddrEnvKey))
	}

	rfIP, ok, err := ipFromEnv(rfAddrEnvKey)
	if err != nil {
		panic(err)
	}
	if !ok {
		panic(fmt.Errorf("%s value is not provided", rfAddrEnvKey))
	}

	var stcpEntities []string
	stcpTableLenStr := os.Getenv(stcpTableLenEnvKey)
	if stcpTableLenStr != "" {
		stcpTableLen, err := strconv.Atoi(stcpTableLenStr)
		if err != nil {
			panic(fmt.Errorf("error getting STCP table len: %w", err))
		}

		stcpEntities = make([]string, 0, stcpTableLen)
		for i := 0; i < stcpTableLen; i++ {
			stcpKey := os.Getenv(stcpKeyEnvPrefix + strconv.Itoa(i))
			if stcpKey == "" {
				panic(fmt.Errorf("STCP table key %d is missing", i))
			}

			stcpAddr := os.Getenv(stcpValueEnvPrefix + stcpKey)
			if stcpAddr == "" {
				panic(fmt.Errorf("STCP table is missing address for key %s", stcpKey))
			}

			stcpEntities = append(stcpEntities, stcpAddr)
		}
	}

	var hypervisorAddrs []string
	hypervisorsCountStr := os.Getenv(hypervisorsCountEnvKey)
	if hypervisorsCountStr != "" {
		hypervisorsCount, err := strconv.Atoi(hypervisorsCountStr)
		if err != nil {
			panic(fmt.Errorf("error getting hypervisors count: %w", err))
		}

		hypervisorAddrs = make([]string, 0, hypervisorsCount)
		for i := 0; i < hypervisorsCount; i++ {
			hypervisorAddr := os.Getenv(hypervisorAddrEnvPrefix + strconv.Itoa(i))
			if hypervisorAddr == "" {
				panic(fmt.Errorf("hypervisor %d addr is missing", i))
			}

			hypervisorAddrs = append(hypervisorAddrs, hypervisorAddr)
		}
	}

	ifc, err := water.New(water.Config{
		DeviceType:             water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{},
	})
	if nil != err {
		log.Fatalln("Error allocating TUN interface:", err)
	}

	fmt.Printf("Allocated TUN %s\n", ifc.Name())

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
	addRoute(dmsgDiscIP.String(), defaultGatewayIP.String(), "")
	addRoute(dmsgIP.String(), defaultGatewayIP.String(), "")
	addRoute(tpDiscIP.String(), defaultGatewayIP.String(), "")
	addRoute(rfIP.String(), defaultGatewayIP.String(), "")

	for _, stcpEntity := range stcpEntities {
		addRoute(stcpEntity, defaultGatewayIP.String(), "")
	}

	for _, hypervisorAddr := range hypervisorAddrs {
		addRoute(hypervisorAddr, defaultGatewayIP.String(), "")
	}

	defer func() {
		deleteRoute(dmsgDiscIP.String(), defaultGatewayIP.String(), "")
		deleteRoute(dmsgIP.String(), defaultGatewayIP.String(), "")
		deleteRoute(tpDiscIP.String(), defaultGatewayIP.String(), "")
		deleteRoute(rfIP.String(), defaultGatewayIP.String(), "")

		for _, stcpEntity := range stcpEntities {
			deleteRoute(stcpEntity, defaultGatewayIP.String(), "")
		}

		for _, hypervisorAddr := range hypervisorAddrs {
			deleteRoute(hypervisorAddr, defaultGatewayIP.String(), "")
		}

		// remove main route
		deleteRoute(ipv4FirstHalfAddr, tunGateway, ipv4HalfRangeMask)
		deleteRoute(ipv4SecondHalfAddr, tunGateway, ipv4HalfRangeMask)
	}()

	// route all traffic through TUN gateway
	addRoute(ipv4FirstHalfAddr, tunGateway, ipv4HalfRangeMask)
	addRoute(ipv4SecondHalfAddr, tunGateway, ipv4HalfRangeMask)

	appCfg, err := app.ClientConfigFromEnv()
	if err != nil {
		log.Fatalf("Error getting client config: %v\n", err)
	}

	vpnClient, err := app.NewClient(logging.MustGetLogger(fmt.Sprintf("app_%s", appName)), appCfg)
	if err != nil {
		log.Fatal("VPN client setup failure: ", err)
	}
	defer func() {
		vpnClient.Close()
	}()

	// setup listener to get incoming routing changes from the visor
	serviceL, err := vpnClient.Listen(netType, servicePort)
	if err != nil {
		panic(err)
	}

	vpnClient.Dial()

	<-shutdownC

	log.Fatalln("DONE")
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
