package skywiremob

import (
	"context"
	"fmt"
	"net"
	"strings"
	"unsafe"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/cmdutil"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/SkycoinProject/skywire-mainnet/internal/vpn"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/visor"
	"github.com/SkycoinProject/skywire-mainnet/pkg/visor/visorconfig"
	"github.com/sirupsen/logrus"
)

// nolint:gosec // https://golang.org/doc/diagnostics.html#profiling

type TestStruct struct {
	data string
}

func (ts *TestStruct) PrintData() string {
	return ts.data
}

func NewTestStruct(d string) *TestStruct {
	t := &TestStruct{data: d}

	return t
}

func NewTestStructInt(d string) int {
	t := &TestStruct{data: d}

	tPtr := int(uintptr(unsafe.Pointer(t)))

	return tPtr
}

func PrintData(ts *TestStruct) string {
	return ts.PrintData()
}

func PrintDataInt(ptrInt int) string {
	ptr := unsafe.Pointer(uintptr(ptrInt))
	ts := (*TestStruct)(ptr)

	return ts.PrintData()
}

func PrintString(str string) {
	fmt.Println(str)
}

func IPs() string {
	ips, err := net.LookupIP("address.resolver.skywire.cc")
	if err != nil {
		fmt.Printf("DICK : PANIC: %v\n", err)
		return ""
	}
	ipsStr := make([]string, 0, len(ips))
	for _, ip := range ips {
		ipsStr = append(ipsStr, ip.String())
	}

	return strings.Join(ipsStr, ";")
}

const (
	visorConfig = `{
	"version": "v1.0.0",
	"sk": "c5b5c8b68ce91dd42bf0343926c7b551336c359c8e13a83cedd573d377aacf8c",
	"pk": "0305deabe88b41b25697ee30133e514bd427be208f4590bc85b27cd447b19b1538",
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
				"name": "skychat",
				"args": [
					"-addr",
					":8001"
				],
				"auto_start": true,
				"port": 1
			},
			{
				"name": "skysocks",
				"auto_start": true,
				"port": 3
			},
			{
				"name": "skysocks-client",
				"auto_start": false,
				"port": 13
			},
			{
				"name": "vpn-server",
				"auto_start": false,
				"port": 44
			},
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

var log *logging.MasterLogger
var globalVisor *visor.Visor

func PrepareTest() string {
	return "TEST"
}

func PrepareLogger() {
	log = logging.NewMasterLogger()
}

func PrepareVisor() {
	conf, err := initConfig(log, "./skywire-config.json")
	if err != nil {
		fmt.Printf("Error getting visor config: %v\n", err)
		return
	}

	v, ok := visor.NewVisor(conf)
	if !ok {
		log.Fatal("Failed to start visor.")
	}

	globalVisor = v
}

func PrepareVPNClient() {
	vpnSrvPKStr := "03d65df7e74a480ab645ade9ae45ec6280c9a86fef2a9e955d99361c9a678b61ee"
	var vpnSrvPK cipher.PubKey
	if err := vpnSrvPK.UnmarshalText([]byte(vpnSrvPKStr)); err != nil {
		log.WithError(err).Fatalln("Invalid VPN Server PK")
	}

	if _, err := globalVisor.SaveTransport(context.Background(), vpnSrvPK, dmsg.Type); err != nil {
		fmt.Printf("ERROR SAVING TRANSPORT TO VPN SERVER: %v\n", err)
	} else {
		fmt.Println("SAVED TRANSPORT TO VPN SERVER")
	}

	vpnPort := routing.Port(skyenv.VPNServerPort)

	connRaw, err := appnet.Dial(appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: vpnSrvPK,
		Port:   vpnPort,
	})
	if err != nil {
		log.Errorf("ERROR DIALING VPN SERVER: %v", err)
		return
	} else {
		log.Infoln("DIALED VPN SERVER")
	}

	conn, err := appnet.WrapConn(connRaw)
	if err != nil {
		log.Errorf("ERROR WRAPPING APP CONN: %v", err)
		return
	} else {
		log.Infoln("WRAPPED APP CONN")
	}

	localPK := cipher.PubKey{}
	if err := localPK.UnmarshalText([]byte("0305deabe88b41b25697ee30133e514bd427be208f4590bc85b27cd447b19b1538")); err != nil {
		log.WithError(err).Fatalln("Invalid local PK")
	}

	localSK := cipher.SecKey{}
	if err := localSK.UnmarshalText([]byte("c5b5c8b68ce91dd42bf0343926c7b551336c359c8e13a83cedd573d377aacf8c")); err != nil {
		log.WithError(err).Fatalln("Invalid local SK")
	}

	noiseCreds := vpn.NewNoiseCredentials(localSK, localPK)

	vpnClientCfg := vpn.ClientConfig{
		Passcode:    "1234",
		Credentials: noiseCreds,
	}

	log2 := logrus.New()
	vpnCl, err := vpn.NewClientMobile(vpnClientCfg, log2, conn)
	if err != nil {
		log.WithError(err).Fatalln("Error creating VPN client")
	} else {
		log.Infoln("CREATED VPN CLIENT")
	}

	vpnClient = vpnCl
}

var (
	vpnClient  *vpn.Client
	tunIP      net.IP
	tunGateway net.IP
	encrypt    bool
)

func ShakeHands() {
	var err error
	tunIP, tunGateway, encrypt, err = vpnClient.ShakeHands()
	if err != nil {
		fmt.Printf("ERROR SHAKING HANDS: %v\n", err)
	} else {
		fmt.Println("SHOOK HANDS")
	}
}

func TUNIP() string {
	return tunIP.String()
}

func TUNGateway() string {
	return tunGateway.String()
}

func VPNEncrypt() bool {
	return encrypt
}

func WaitForVisorToStop() {
	ctx, cancel := cmdutil.SignalContext(context.Background(), log)
	defer cancel()

	// Wait.
	<-ctx.Done()

	if err := globalVisor.Close(); err != nil {
		log.WithError(err).Error("Visor closed with error.")
	}
}

func initConfig(mLog *logging.MasterLogger, confPath string) (*visorconfig.V1, error) {
	conf, err := visorconfig.Parse(mLog, confPath, []byte(visorConfig))
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return conf, nil
}
