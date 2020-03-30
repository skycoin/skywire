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
	"sync"
	"syscall"

	"github.com/songgao/water"
)

const (
	dmsgDiscAddrEnvKey = "ADDR_DMSG_DISC"
	dmsgAddrEnvKey     = "ADDR_DMSG_SRV"
	tpDiscAddrEnvKey   = "ADDR_TP_DISC"
	rfAddrEnvKey       = "ADDR_RF"
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

type RouteArg struct {
	IP      string
	Gateway string
	Netmask string
}

var nonRoutedTrafficMx sync.Mutex
var nonRoutedTraffic []RouteArg

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

	dmsgDiscIP, err := ipFromEnv(dmsgDiscAddrEnvKey)
	if err != nil {
		panic(err)
	}

	dmsgIP, err := ipFromEnv(dmsgAddrEnvKey)
	if err != nil {
		panic(err)
	}

	tpDiscIP, err := ipFromEnv(tpDiscAddrEnvKey)
	if err != nil {
		panic(err)
	}

	rfIP, err := ipFromEnv(rfAddrEnvKey)
	if err != nil {
		panic(err)
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

		if err := shutdown(); err != nil {
			log.Printf("Shutdown error: %v\n", err)
		}

		shutdownC <- struct{}{}
	}()

	//time.Sleep(10 * time.Minute)

	setupTUN(ifc.Name(), tunIP, tunNetmask, tunGateway, tunMTU)

	// route Skywire service traffic through the default gateway
	nonRoutedTrafficMx.Lock()
	addRoute(dmsgDiscIP.String(), defaultGatewayIP.String(), "")
	addRoute(dmsgIP.String(), defaultGatewayIP.String(), "")
	addRoute(tpDiscIP.String(), defaultGatewayIP.String(), "")
	addRoute(rfIP.String(), defaultGatewayIP.String(), "")
	nonRoutedTraffic = append(nonRoutedTraffic, RouteArg{
		IP:      dmsgDiscIP.String(),
		Gateway: defaultGatewayIP.String(),
		Netmask: "",
	})
	nonRoutedTraffic = append(nonRoutedTraffic, RouteArg{
		IP:      dmsgIP.String(),
		Gateway: defaultGatewayIP.String(),
		Netmask: "",
	})
	nonRoutedTraffic = append(nonRoutedTraffic, RouteArg{
		IP:      tpDiscIP.String(),
		Gateway: defaultGatewayIP.String(),
		Netmask: "",
	})
	nonRoutedTraffic = append(nonRoutedTraffic, RouteArg{
		IP:      rfIP.String(),
		Gateway: defaultGatewayIP.String(),
		Netmask: "",
	})
	nonRoutedTrafficMx.Unlock()

	// route all traffic through TUN gateway
	addRoute(ipv4FirstHalfAddr, tunGateway, ipv4HalfRangeMask)
	addRoute(ipv4SecondHalfAddr, tunGateway, ipv4HalfRangeMask)

	//run("addr", "add", localSubnet, "dev", ifc.Name())
	//run("link", "set", "dev", ifc.Name(), "up")

	//time.Sleep(10 * time.Minute)

	<-shutdownC

	log.Fatalln("DONE")
}

func shutdown() error {
	// remove all routes for direct traffic
	nonRoutedTrafficMx.Lock()
	for _, arg := range nonRoutedTraffic {
		deleteRoute(arg.IP, arg.Gateway, arg.Netmask)
	}
	nonRoutedTraffic = nil
	nonRoutedTrafficMx.Unlock()

	// remove main route
	deleteRoute(ipv4FirstHalfAddr, tunGateway, ipv4HalfRangeMask)
	deleteRoute(ipv4SecondHalfAddr, tunGateway, ipv4HalfRangeMask)

	return nil
}

func ipFromEnv(key string) (net.IP, error) {
	addr := os.Getenv(key)
	if addr == "" {
		return nil, fmt.Errorf("value for key %s is not provided in env", key)
	}

	ip := net.ParseIP(addr)
	if ip != nil {
		return ip, nil
	}

	ips, err := net.LookupIP(addr)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("couldn't resolve IPs of %s", addr)
	}

	// initially take just the first one
	ip = ips[0]

	return ip, nil
}
