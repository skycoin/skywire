// Package skyenv defines variables and constants for different operating systems
package skyenv

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

const (
	// ConfigName is the default config name. Updated by setting config file path.
	ConfigName = "skywire-config.json"

	// DMSGHTTPName is the default dmsghttp config name
	DMSGHTTPName = "dmsghttp-config.json"
)

// Constants for skywire root directories.
// Dmsg port constants.
// TODO(evanlinjin): Define these properly. These are currently random.
const (
	DmsgCtrlPort           uint16 = 7                        // Listening port for dmsgctrl protocol (similar to TCP Echo Protocol).
	DmsgSetupPort          uint16 = 36                       // Listening port of a setup node.
	DmsgHypervisorPort     uint16 = 46                       // Listening port of a hypervisor for incoming RPC visor connections over dmsg.
	DmsgTransportSetupPort uint16 = 47                       // Listening port for transport setup RPC over dmsg.
	DmsgHTTPPort           uint16 = dmsg.DefaultDmsgHTTPPort // Listening port for dmsghttp logserver.
	DmsgAwaitSetupPort     uint16 = 136                      // Listening port of a visor for setup operations.
)

// Transport port constants.
const (
	TransportPort     uint16 = 45 // Listening port of a visor for incoming transports.
	PublicAutoconnect        = true
)

// Dmsgpty constants.
const (
	DmsgPtyPort   uint16 = 22
	DmsgPtyCLINet        = "unix"
)

// Skywire-TCP constants.
const (
	STCPAddr = ":7777"
)

// Default skywire app constants.
const (
	SkychatName        = "skychat"
	SkychatPort uint16 = 1
	SkychatAddr        = ":8001"

	PingTestName        = "pingtest"
	PingTestPort uint16 = 2

	SkysocksName        = "skysocks"
	SkysocksPort uint16 = 3

	SkysocksClientName        = "skysocks-client"
	SkysocksClientPort uint16 = 13
	SkysocksClientAddr        = ":1080"

	VPNServerName        = "vpn-server"
	VPNServerPort uint16 = 44

	VPNClientName = "vpn-client"
	// TODO(darkrengarius): this one's not needed for the app to run but lack of it causes errors
	VPNClientPort uint16 = 43

	SkyPingName        = "sky-ping"
	SkyPingPort uint16 = 48
)

// RPC constants.
const (
	RPCAddr             = "localhost:3435"
	RPCTimeout          = 20 * time.Second
	TransportRPCTimeout = 1 * time.Minute
	UpdateRPCTimeout    = 6 * time.Hour // update requires huge timeout
)

// Default skywire app server and discovery constants
const (
	AppSrvAddr                = "localhost:5505"
	ServiceDiscUpdateInterval = time.Minute
	AppBinPath                = "./apps"
	LogLevel                  = "info"
)

// Routing constants
const (
	TpLogStore = "transport_logs"
	Custom     = "custom"
)

// Local constants
const (
	LocalPath = "./local"
)

// Default hypervisor constants
const (
	HypervisorDB      = ".skycoin/hypervisor/users.db"
	EnableAuth        = false
	PackageEnableAuth = true
	EnableTLS         = false
	TLSKey            = "./ssl/key.pem"
	TLSCert           = "./ssl/cert.pem"
)

const (
	// IPCShutdownMessageType sends IPC shutdown message type
	IPCShutdownMessageType = 68
)

const (
	//IsPublic advertises the visor in the service discovery
	IsPublic = false
)

// PkgConfig struct contains paths specific to the linux packages
type PkgConfig struct {
	Launcher   `json:"launcher"`
	LocalPath  string `json:"local_path"`
	Hypervisor `json:"hypervisor"`
	//		TLSCertFile string `json:"tls_cert_file"`
	//		TLSKeyFile  string `json:"tls_key_file"`
}

// Launcher struct contains the BinPath specific to the linux packages
type Launcher struct {
	BinPath string `json:"bin_path"`
}

// Hypervisor struct contains Hypervisor paths specific to the linux packages
type Hypervisor struct {
	DbPath     string `json:"db_path"`
	EnableAuth bool   `json:"enable_auth"`
}

// DmsgPtyWhiteList gets dmsgpty whitelist path for installed Skywire.
func DmsgPtyWhiteList() string {
	return filepath.Join(SkywirePath, "dmsgpty", "whitelist.json")
}

// MustPK unmarshals string PK to cipher.PubKey. It panics if unmarshaling fails.
func MustPK(pk string) cipher.PubKey {
	var sPK cipher.PubKey
	if err := sPK.UnmarshalText([]byte(pk)); err != nil {
		panic(err)
	}

	return sPK
}

// Version gets the version of the installation for the config
func Version() string {
	u := buildinfo.Version()
	v := u
	if u == "unknown" {
		//check for .git folder for versioning
		if _, err := os.Stat(".git"); err == nil {
			//attempt to version from git sources
			if _, err = exec.LookPath("git"); err == nil {
				if v, err = script.Exec(`git describe`).String(); err == nil {
					v = strings.ReplaceAll(v, "\n", "")
					v = strings.Split(v, "-")[0]
				}
			}
		}
	}
	return v
}

// HomePath gets the current user's home folder
func HomePath() string {
	dir, _ := os.UserHomeDir() //nolint
	return dir
}

// Config returns either UserConfig or PackageConfig based on permissions
func Config() PkgConfig {
	if IsRoot() {
		return PackageConfig()
	}
	return UserConfig()
}

// IsRoot checks for root permissions
func IsRoot() bool {
	userLvl, _ := user.Current() //nolint
	return userLvl.Username == "root"
}

// IPAddr struct of `ip --json addr`
type IPAddr []struct {
	Ifindex   int      `json:"ifindex"`
	Ifname    string   `json:"ifname"`
	Flags     []string `json:"flags"`
	Mtu       int      `json:"mtu"`
	Qdisc     string   `json:"qdisc"`
	Operstate string   `json:"operstate"`
	Group     string   `json:"group"`
	Txqlen    int      `json:"txqlen"`
	LinkType  string   `json:"link_type"`
	Address   string   `json:"address"`
	Broadcast string   `json:"broadcast"`
	AddrInfo  []struct {
		Family            string `json:"family"`
		Local             string `json:"local"`
		Prefixlen         int    `json:"prefixlen"`
		Scope             string `json:"scope"`
		Label             string `json:"label,omitempty"`
		ValidLifeTime     int64  `json:"valid_life_time"`
		PreferredLifeTime int64  `json:"preferred_life_time"`
	} `json:"addr_info"`
}

// IPA returns IPAddr struct filled in with the json response from `ip --json addr` command ; fail silently on errors
func IPA() (ip *IPAddr) {
	//non-critical logic implemented with bitfield/script
	ipa, err := script.Exec(`ip --json addr`).String()
	if err != nil {
		return nil
	}
	err = json.Unmarshal([]byte(ipa), &ip)
	if err != nil {
		return nil
	}
	return ip
}

// IPSkycoin struct of ip.skycoin.com json
type IPSkycoin struct {
	IPAddress     string  `json:"ip_address"`
	Latitude      float64 `json:"latitude"`
	Longitude     float64 `json:"longitude"`
	PostalCode    string  `json:"postal_code"`
	ContinentCode string  `json:"continent_code"`
	CountryCode   string  `json:"country_code"`
	CountryName   string  `json:"country_name"`
	RegionCode    string  `json:"region_code"`
	RegionName    string  `json:"region_name"`
	ProvinceCode  string  `json:"province_code"`
	ProvinceName  string  `json:"province_name"`
	CityName      string  `json:"city_name"`
	Timezone      string  `json:"timezone"`
}

// IPSkycoinFetch fetches the json response from ip.skycoin.com
func IPSkycoinFetch() (ipskycoin *IPSkycoin) {

	url := fmt.Sprint("http://", "ip.skycoin.com")
	client := http.Client{
		Timeout: time.Second * 2, // Timeout after 2 seconds
	}
	//create the http request
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	req.Header.Add("Cache-Control", "no-cache")
	//check for errors in the response
	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	if res.Body != nil {
		defer res.Body.Close() //nolint
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil
	}
	//fill in IPSkycoin struct with the response
	err = json.Unmarshal(body, &ipskycoin)
	if err != nil {
		return nil
	}
	return ipskycoin
}

// SurveyFile is the name of the survey file
const SurveyFile string = "system.json"

// SurveySha256 is the name of the survey checksum file
const SurveySha256 string = "system.sha"

// RewardFile is the name of the file containing skycoin rewards address and privacy setting
const RewardFile string = "reward.txt"
