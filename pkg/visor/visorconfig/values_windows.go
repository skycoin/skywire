//go:build windows
// +build windows

package visorconfig

import (
	"runtime"

	"github.com/google/uuid"
	"github.com/jaypipes/ghw"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// UserConfig contains installation paths for running skywire as the user
func UserConfig() skyenv.PkgConfig {
	usrConfig := skyenv.PkgConfig{
		LauncherBinPath: "C:/Program Files/Skywire/apps",
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
	PubKey         cipher.PubKey    `json:"public_key,omitempty"`
	SkycoinAddress string           `json:"skycoin_address,omitempty"`
	GOOS           string           `json:"go_os,omitempty"`
	GOARCH         string           `json:"go_arch,omitempty"`
	IPInfo         *IPSkycoin       `json:"ip.skycoin.com,omitempty"`
	IPAddr         *IPAddr          `json:"ip_addr,omitempty"`
	Disks          *ghw.BlockInfo   `json:"ghw_blockinfo,omitempty"`
	Product        *ghw.ProductInfo `json:"ghw_productinfo,omitempty"`
	Memory         *ghw.MemoryInfo  `json:"ghw_memoryinfo,omitempty"`
	UUID           uuid.UUID        `json:"uuid,omitempty"`
	SkywireVersion string           `json:"skywire_version,omitempty"`
}

// SystemSurvey returns system survey
func SystemSurvey() (Survey, error) {
	disks, err := ghw.Block(ghw.WithDisableWarnings())
	if err != nil {
		return Survey{}, err
	}
	product, err := ghw.Product(ghw.WithDisableWarnings())
	if err != nil {
		return Survey{}, err
	}
	memory, err := ghw.Memory(ghw.WithDisableWarnings())
	if err != nil {
		return Survey{}, err
	}
	s := Survey{
		IPInfo:         IPSkycoinFetch(),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		UUID:           uuid.New(),
		Disks:          disks,
		Product:        product,
		Memory:         memory,
		SkywireVersion: Version(),
	}
	return s, nil
}
