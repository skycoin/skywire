//go:build darwin
// +build darwin

package visorconfig

import (
	"runtime"

	"github.com/google/uuid"
	"github.com/jaypipes/ghw"

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
	PubKey         cipher.PubKey  `json:"public_key,omitempty"`
	SkycoinAddress string         `json:"skycoin_address,omitempty"`
	GOOS           string         `json:"go_os,omitempty"`
	GOARCH         string         `json:"go_arch,omitempty"`
	IPAddr         string         `json:"ip_address,omitempty"`
	Disks          *ghw.BlockInfo `json:"ghw_blockinfo,omitempty"`
	UUID           uuid.UUID      `json:"uuid,omitempty"`
	SkywireVersion string         `json:"skywire_version,omitempty"`
	ServicesURLs   Services       `json:"services,omitempty"`
	DmsgServers    []string       `json:"dmsg_servers,omitempty"`
}

// SystemSurvey returns system survey
func SystemSurvey(dmsgDisc string) (Survey, error) {
	disks, err := ghw.Block(ghw.WithDisableWarnings())
	if err != nil {
		return Survey{}, err
	}
	var ipAddr string
	for {
		ipAddr, err = FetchIP(dmsgDisc)
		if err == nil {
			break
		}
	}
	s := Survey{
		IPAddr:         ipAddr,
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		UUID:           uuid.New(),
		Disks:          disks,
		SkywireVersion: Version(),
	}
	return s, nil
}
