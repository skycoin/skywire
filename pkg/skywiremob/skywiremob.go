package skywiremob

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/cmdutil"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const (
	visorConfig = `{
	"version": "v1.0.0",
	"sk": "${SK}",
	"pk": "${PK}",
	"dmsg": {
		"discovery": "http://dmsg.discovery.skywire.cc",
		"sessions_count": 1
	},
	"stcp": {
		"pk_table": null,
		"local_address": ":7777"
	},
	"transport": {
		"discovery": "http://transport.discovery.skywire.cc",
		"address_resolver": "http://address.resolver.skywire.cc",
		"log_store": {
			"type": "memory",
			"location": "./transport_logs"
		},
		"trusted_visors": null
	},
	"routing": {
		"setup_nodes": [
			"026c5a07de617c5c488195b76e8671bf9e7ee654d0633933e202af9e111ffa358d"
		],
		"route_finder": "http://routefinder.skywire.cc",
		"route_finder_timeout": "10s"
	},
	"uptime_tracker": {
		"addr": "http://uptime.tracker.skywire.cc"
	},
	"launcher": {
		"discovery": {
			"update_interval": "30s",
			"proxy_discovery_addr": "http://service.discovery.skywire.cc"
		},
		"apps": [
			{
				"name": "vpn-client",
				"auto_start": false,
				"port": 43
			}
		],
		"server_addr": "localhost:5505",
		"bin_path": "./apps",
		"local_path": "./local"
	},
	"hypervisors": [],
	"cli_addr": "localhost:3435",
	"log_level": "info",
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s"
}`
)

// PrintString logs passed `str` with info log level.
func PrintString(str string) {
	log.Infoln(str)
}

// IsPKValid checks if pub key is valid. Returns non-empty error string on failure.
func IsPKValid(pkStr string) string {
	var pk cipher.PubKey
	if err := pk.UnmarshalText([]byte(pkStr)); err != nil {
		return fmt.Errorf("invalid PK: %w", err).Error()
	}

	return ""
}

// GetMTU returns VPN connection MTU.
func GetMTU() int {
	return vpn.TUNMTU
}

// GetTUNIPPrefix returns netmask prefix of TUN IP address.
func GetTUNIPPrefix() int {
	return vpn.TUNNetmaskPrefix
}

var isVPNReady int32

// IsVPNReady checks if the VPN is ready.
func IsVPNReady() bool {
	return atomic.LoadInt32(&isVPNReady) == 1
}

var log = logging.NewMasterLogger()

var (
	globalVisor *visor.Visor

	stopVisorFuncMx sync.Mutex
	stopVisorFunc   func()

	vpnClient  *vpn.ClientMobile
	tunCredsMx sync.Mutex
	tunIP      net.IP
	tunGateway net.IP
)

// PrepareVisor creates and runs visor instance.
func PrepareVisor() string {
	// set the same init stack as usual, but without apps launcher
	visor.SetInitStack(func() []visor.InitFunc {
		return []visor.InitFunc{
			visor.InitUpdater,
			visor.InitEventBroadcaster,
			visor.InitAddressResolver,
			visor.InitDiscovery,
			visor.InitSNet,
			visor.InitDmsgpty,
			visor.InitTransport,
			visor.InitRouter,
			visor.InitNetworkers,
			visor.InitCLI,
			visor.InitHypervisors,
			visor.InitUptimeTracker,
			visor.InitTrustedVisors,
		}
	})

	// we use STDIN not to flush the config
	conf, err := initConfig(log, visorconfig.StdinName)
	if err != nil {
		return fmt.Errorf("error getting visor config: %v", err).Error()
	}

	log.Infoln("Initialized config")

	v, ok := visor.NewVisor(conf, nil)
	if !ok {
		return errors.New("failed to start visor").Error()
	}

	globalVisor = v

	log.Infoln("Started visor")

	return ""
}

var (
	nextDmsgSocketIdxMx sync.Mutex
	nextDmsgSocketIdx   = -1
)

func getNextDmsgSocketIdx(ln int) int {
	nextDmsgSocketIdxMx.Lock()
	defer nextDmsgSocketIdxMx.Unlock()

	if nextDmsgSocketIdx == -2 {
		return -2
	}

	nextDmsgSocketIdx++
	if nextDmsgSocketIdx == ln {
		nextDmsgSocketIdx = -2
		return -2
	}

	return nextDmsgSocketIdx
}

// NextDmsgSocket returns next file descriptor of Dmsg socket. If no descriptors
// left or in case of error returns 0.
func NextDmsgSocket() int {
	allSessions := globalVisor.Network().Dmsg().AllSessions()
	log.Infof("Dmsg sockets count: %d\n", len(allSessions))

	nextDmsgSocketIdx := getNextDmsgSocketIdx(len(allSessions))
	if nextDmsgSocketIdx == -2 {
		return 0
	}

	conn := allSessions[nextDmsgSocketIdx].SessionCommon.GetConn()

	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		log.Infoln("Failed to get Dmsg TCP conn")
		return 0
	}

	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		log.Infof("Failed to get Dmsg raw conn: %v\n", err)
		return 0
	}

	var fd uintptr
	var controlFunc = func(fdInner uintptr) {
		fd = fdInner
	}

	if err := rawConn.Control(controlFunc); err != nil {
		log.Infof("Failed to get Dmsg FD: %v\n", err)
		return 0
	}

	return int(fd)
}

// PrepareVPNClient creates and runs VPN client instance.
func PrepareVPNClient(srvPKStr, passcode string) string {
	var srvPK cipher.PubKey
	if err := srvPK.UnmarshalText([]byte(srvPKStr)); err != nil {
		return fmt.Errorf("invalid remote PK: %w", err).Error()
	}

	if _, err := globalVisor.SaveTransport(context.Background(), srvPK, dmsg.Type); err != nil {
		return fmt.Errorf("failed to save transport to VPN server: %w", err).Error()
	}

	log.Infoln("Saved transport to VPN server")

	vpnPort := routing.Port(skyenv.VPNServerPort)

	connRaw, err := appnet.Dial(appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: srvPK,
		Port:   vpnPort,
	})
	if err != nil {
		return fmt.Errorf("failed to dial VPN server: %w", err).Error()
	}

	log.Infoln("Dialed VPN server")

	conn, err := appnet.WrapConn(connRaw)
	if err != nil {
		return fmt.Errorf("failed to wrap app conn: %w", err).Error()
	}

	log.Infoln("Wrapped app conn")

	vpnClientCfg := vpn.ClientConfig{
		Passcode: passcode,
	}

	vpnCl, err := vpn.NewClientMobile(vpnClientCfg, logrus.New(), conn)
	if err != nil {
		return fmt.Errorf("failed to create VPN client: %w", err).Error()
	}

	vpnClient = vpnCl

	log.Infoln("Created VPN client")

	return ""
}

// ShakeHands performs VPN client/server handshake.
func ShakeHands() string {
	tunIPInternal, tunGatewayInternal, err := vpnClient.ShakeHands()
	if err != nil {
		return fmt.Errorf("handshake error: %w", err).Error()
	}

	log.Infoln("Shook hands with VPN server")

	tunCredsMx.Lock()
	tunIP = tunIPInternal
	tunGateway = tunGatewayInternal
	tunCredsMx.Unlock()

	log.Println("Set TUN IP and gateway")

	return ""
}

// TUNIP gets assigned TUN IP address.
func TUNIP() string {
	tunCredsMx.Lock()
	defer tunCredsMx.Unlock()

	if tunIP == nil {
		return ""
	}

	return tunIP.String()
}

// TUNGateway gets assigned TUN gateway.
func TUNGateway() string {
	tunCredsMx.Lock()
	defer tunCredsMx.Unlock()

	if tunGateway == nil {
		return ""
	}

	return tunGateway.String()
}

// StopVisor stops running visor. Returns non-empty error string on failure.
func StopVisor() string {
	stopVisorFuncMx.Lock()
	stopFunc := stopVisorFunc
	stopVisorFunc = nil
	stopVisorFuncMx.Unlock()

	if stopFunc == nil {
		return "visor is not running"
	}

	stopFunc()

	vpnClient.Close()
	if err := udpConn.Close(); err != nil {
		log.WithError(err).Errorln("Failed to close mobile app UDP conn")
	}

	return ""
}

// WaitForVisorToStop blocks until visor exits. Returns non-empty error string on failure.
func WaitForVisorToStop() string {
	ctx, cancel := cmdutil.SignalContext(context.Background(), log)
	stopVisorFuncMx.Lock()
	stopVisorFunc = cancel
	stopVisorFuncMx.Unlock()

	// Wait.
	<-ctx.Done()

	if err := globalVisor.Close(); err != nil {
		return fmt.Errorf("failed to close visor: %w", err).Error()
	}

	return ""
}

func initConfig(mLog *logging.MasterLogger, confPath string) (*visorconfig.V1, error) {
	pk, sk := cipher.GenerateKeyPair()

	parsedConf := strings.ReplaceAll(visorConfig, "${SK}", sk.String())
	parsedConf = strings.ReplaceAll(parsedConf, "${PK}", pk.String())
	conf, err := visorconfig.Parse(mLog, confPath, []byte(parsedConf))
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return conf, nil
}

var mobileAppAddrCh = make(chan *net.UDPAddr, 2)

// SetMobileAppAddr sets address of the UDP connection opened on the mobile application side.
func SetMobileAppAddr(addr string) {
	// address passed from the Android device contains `/` prefix, strip it.
	addr = strings.TrimLeft(addr, " /")

	tokens := strings.Split(addr, ":")

	addrIP := net.ParseIP(tokens[0])
	addrPort, err := strconv.Atoi(tokens[1])
	if err != nil {
		log.WithError(err).Errorln("Failed to parse android app port")
		return
	}

	mobileAppAddrCh <- &net.UDPAddr{
		IP:   addrIP,
		Port: addrPort,
		Zone: "",
	}
	close(mobileAppAddrCh)
}

var udpConn *net.UDPConn

// ServeVPN starts handling VPN traffic.
func ServeVPN() {
	go func() {
		tunAddr := <-mobileAppAddrCh
		log.Infof("Got mobile app UDP addr: %s\n", tunAddr.String())
		wr := vpn.NewUDPConnWriter(udpConn, tunAddr)

		if err := vpnClient.Serve(wr); err != nil {
			log.WithError(err).Errorln("Failed to serve VPN")
		}
	}()

	atomic.StoreInt32(&isVPNReady, 1)
}

// StartListeningUDP starts listening UDP.
func StartListeningUDP() string {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{
		IP:   net.IP{127, 0, 0, 1},
		Port: 7890,
	})
	if err != nil {
		return fmt.Errorf("failed to listen UDP: %w", err).Error()
	}

	udpConn = conn

	log.Infoln("Listening UDP")

	return ""
}
