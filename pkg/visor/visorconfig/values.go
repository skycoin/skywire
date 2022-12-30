// Package visorconfig defines variables and constants for different operating systems
package visorconfig

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
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	// config file constants
	// ConfigName is the default config name. Updated by setting config file path.
	ConfigName = skyenv.ConfigName
	// DMSGHTTPName is the default dmsghttp config name
	DMSGHTTPName = skyenv.DMSGHTTPName

	// Dmsg port constants.
	// TODO(evanlinjin): Define these properly. These are currently random.
	DmsgCtrlPort           = skyenv.DmsgCtrlPort           // Listening port for dmsgctrl protocol (similar to TCP Echo Protocol).
	DmsgSetupPort          = skyenv.DmsgSetupPort          // Listening port of a setup node.
	DmsgHypervisorPort     = skyenv.DmsgHypervisorPort     // Listening port of a hypervisor for incoming RPC visor connections over dmsg.
	DmsgTransportSetupPort = skyenv.DmsgTransportSetupPort // Listening port for transport setup RPC over dmsg.
	DmsgHTTPPort           = dmsg.DefaultDmsgHTTPPort      // Listening port for dmsghttp logserver.
	DmsgAwaitSetupPort     = skyenv.DmsgAwaitSetupPort     // Listening port of a visor for setup operations.

	// Transport port constants.
	TransportPort     = skyenv.TransportPort // Listening port of a visor for incoming transports.
	PublicAutoconnect = skyenv.PublicAutoconnect

	// Dmsgpty constants.
	DmsgPtyPort   = skyenv.DmsgPtyPort
	DmsgPtyCLINet = skyenv.DmsgPtyCLINet

	// Skywire-TCP constants.
	STCPAddr = skyenv.STCPAddr

	// Default skywire app constants.
	SkychatName = skyenv.SkychatName
	SkychatPort = skyenv.SkychatPort
	SkychatAddr = skyenv.SkychatAddr

	SkysocksName = skyenv.SkysocksName
	SkysocksPort = skyenv.SkysocksPort

	SkysocksClientName = skyenv.SkysocksClientName
	SkysocksClientPort = skyenv.SkysocksClientPort
	SkysocksClientAddr = skyenv.SkysocksClientAddr

	VPNServerName = skyenv.VPNServerName
	VPNServerPort = skyenv.VPNServerPort

	VPNClientName = skyenv.VPNClientName

	// TODO(darkrengarius): this one's not needed for the app to run but lack of it causes errors
	VPNClientPort = skyenv.VPNClientPort

	// RPC constants.
	RPCAddr             = skyenv.RPCAddr
	RPCTimeout          = skyenv.RPCTimeout
	TransportRPCTimeout = skyenv.TransportRPCTimeout
	UpdateRPCTimeout    = skyenv.UpdateRPCTimeout

	// Default skywire app server and discovery constants
	AppSrvAddr                = skyenv.AppSrvAddr
	ServiceDiscUpdateInterval = skyenv.ServiceDiscUpdateInterval
	AppBinPath                = skyenv.AppBinPath
	LogLevel                  = skyenv.LogLevel

	// Routing constants
	TpLogStore = skyenv.TpLogStore
	Custom     = skyenv.Custom

	// Local constants
	LocalPath = skyenv.LocalPath

	// Default hypervisor constants
	HypervisorDB      = skyenv.HypervisorDB
	EnableAuth        = skyenv.EnableAuth
	PackageEnableAuth = skyenv.PackageEnableAuth
	EnableTLS         = skyenv.EnableTLS
	TLSKey            = skyenv.TLSKey
	TLSCert           = skyenv.TLSCert

	// IPCShutdownMessageType sends IPC shutdown message type
	IPCShutdownMessageType = skyenv.IPCShutdownMessageType

	// IsPublic advertises the visor in the service discovery
	IsPublic = skyenv.IsPublic

	// SurveyFile is the name of the survey file
	SurveyFile = skyenv.SurveyFile

	// SurveySha256 is the name of the survey checksum file
	SurveySha256 = skyenv.SurveySha256

	// RewardFile is the name of the file containing skycoin rewards address and privacy setting
	RewardFile = skyenv.RewardFile
)

// PkgConfig struct contains paths specific to the linux packages
type PkgConfig struct {
	LauncherBinPath string `json:"launcher"`
	LocalPath       string `json:"local_path"`
	Hypervisor      `json:"hypervisor"`
	//		TLSCertFile string `json:"tls_cert_file"`
	//		TLSKeyFile  string `json:"tls_key_file"`
}

// Launcher struct contains the BinPath specific to the installation
type LauncherBinPath struct {
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

var (
	// VisorConfigFile will contain the path to the visor's config or `stdin` to denote that the config was read from STDIN
	VisorConfigFile string
)
