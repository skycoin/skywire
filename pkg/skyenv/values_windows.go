//go:build windows
// +build windows

package skyenv

import (
	"runtime"

	"github.com/google/uuid"
	"github.com/jaypipes/ghw"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

const (
	//OS detection at runtime
	OS = "win"
	// SkywirePath is the path to the installation folder for the .msi
	SkywirePath = "C:/Program Files/Skywire"
	// ConfigJSON is the config name generated by the batch file included with the windows .msi
	ConfigJSON = ConfigName

	//TODO: @mrpalide set this correctly for windows. it shouldn't be in the installed path

	// SkyenvFilePath is the path tothe SkyenvFile
	SkyenvFilePath = "C:/Program Files/Skywire"
	// SkyenvFile contains environmental variables which are detected by `skywire-autoconfig` / `skywire-cli config auto` to set default or persistent values
	SkyenvFile = "skyenv.bat"
)

func SkywireConfig() string {
	return SkyenvFilePath + "/" + ConfigJSON
}

func SkyEnvs() string {
	return SkyenvFilePath + "/" + SkyenvFile
}

// PackageConfig contains installation paths (for windows)
func PackageConfig() PkgConfig {
	pkgConfig := PkgConfig{
		Launcher: Launcher{
			BinPath: "C:/Program Files/Skywire/apps",
		},
		LocalPath: "C:/Program Files/Skywire/local",
		Hypervisor: Hypervisor{
			DbPath:     "C:/Program Files/Skywire/users.db",
			EnableAuth: true,
		},
	}
	return pkgConfig
}

// UserConfig contains installation paths (for windows)
func UserConfig() PkgConfig {
	usrConfig := PkgConfig{
		Launcher: Launcher{
			BinPath: "C:/Program Files/Skywire/apps",
		},
		LocalPath: HomePath() + "/.skywire/local",
		Hypervisor: Hypervisor{
			DbPath:     HomePath() + "/.skywire/users.db",
			EnableAuth: true,
		},
	}
	return usrConfig
}

// UpdateCommand returns the commands which are run when the update button is clicked in the ui
func UpdateCommand() []string {
	return []string{`echo "Update not implemented for windows. Download a new version from the release section here: https://github.com/skycoin/skywire/releases"`}
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
	disks, err := ghw.Block()
	if err != nil {
		return Survey{}, err
	}
	product, err := ghw.Product()
	if err != nil {
		return Survey{}, err
	}
	memory, err := ghw.Memory()
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
