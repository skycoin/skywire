//go:build darwin
// +build darwin

package visorconfig

import (
	"net"
	"os/user"
	"runtime"
	"time"

	"github.com/google/uuid"
	"github.com/jaypipes/ghw"
	"github.com/zcalusic/sysinfo"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// UserConfig contains installation paths for running skywire as the user
func UserConfig() skyenv.PkgConfig {
	usrConfig := skyenv.PkgConfig{
		LauncherBinPath: "/Applications/Skywire.app/Contents/MacOS",
		LocalPath:       HomePath() + "/.skywire/local",
		Hypervisor: skyenv.Hypervisor{
			DbPath:     HomePath() + "/.skywire/users.db",
			EnableAuth: true,
		},
	}
	return usrConfig
}

// Survey system hardware survey struct
type Survey struct {
	Timestamp      time.Time      `json:"timestamp"`
	PubKey         cipher.PubKey  `json:"public_key,omitempty"`
	SkycoinAddress string         `json:"skycoin_address,omitempty"`
	GOOS           string         `json:"go_os,omitempty"`
	GOARCH         string         `json:"go_arch,omitempty"`
	SYSINFO        customSysinfo  `json:"zcalusic_sysinfo,omitempty"`
	IPAddr         string         `json:"ip_address,omitempty"`
	Disks          *ghw.BlockInfo `json:"ghw_blockinfo,omitempty"`
	UUID           uuid.UUID      `json:"uuid,omitempty"`
	SkywireVersion string         `json:"skywire_version,omitempty"`
	ServicesURLs   Services       `json:"services,omitempty"`
	DmsgServers    []string       `json:"dmsg_servers,omitempty"`
}

// SystemSurvey returns system survey
func SystemSurvey() (Survey, error) {
	disks, err := ghw.Block(ghw.WithDisableWarnings())
	if err != nil {
		return Survey{}, err
	}
	s := Survey{
		Timestamp:      time.Now(),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		SYSINFO:        getMacAddr(),
		UUID:           uuid.New(),
		Disks:          disks,
		SkywireVersion: Version(),
	}
	return s, nil
}

// IsRoot checks for root permissions
func IsRoot() bool {
	userLvl, _ := user.Current() //nolint
	return userLvl.Username == "root"
}

type customSysinfo struct {
	Network []sysinfo.NetworkDevice `json:"network,omitempty"`
}

func getMacAddr() customSysinfo {
	var sysInfo customSysinfo
	si := make([]sysinfo.NetworkDevice, 1)
	interfaces, err := net.Interfaces()
	if err != nil {
		return sysInfo
	}

	for _, ifa := range interfaces {
		si[0].MACAddress = ifa.HardwareAddr.String()
		if si[0].MACAddress != "" {
			sysInfo.Network = si
			return sysInfo
		}
	}
	return sysInfo
}
