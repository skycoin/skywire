//go:build linux
// +build linux

package skyenv

import (
	"runtime"

	"github.com/google/uuid"
	"github.com/jaypipes/ghw"
	"github.com/zcalusic/sysinfo"
	"periph.io/x/periph/host/distro"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

const (
	//OS detection at runtime
	OS = "linux"
	// SkywirePath is the path to the installation folder for the linux packages.
	SkywirePath = "/opt/skywire"
	// ConfigJSON is the config name generated by the skywire-autocofig script in the linux packages
	ConfigJSON = "skywire.json"
	// SkyenvFilePath is the path to the SkyenvFile
	SkyenvFilePath = "/etc/profile.d"
	// SkyenvFile contains environmental variables which are detected by `skywire-autoconfig` / `skywire-cli config auto` to set default or persistent values
	SkyenvFile = "skyenv.sh"
)

// SkywireConfig returns the full path to the package config
func SkywireConfig() string {
	return SkywirePath + "/" + ConfigJSON
}

// SkyEnvs returns the full path to the environmental variable file
func SkyEnvs() string {
	return SkyenvFilePath + "/" + SkyenvFile
}

// PackageConfig contains installation paths (for linux)
func PackageConfig() PkgConfig {
	pkgConfig := PkgConfig{
		Launcher: Launcher{
			BinPath: "/opt/skywire/apps",
		},
		LocalPath: "/opt/skywire/local",
		Hypervisor: Hypervisor{
			DbPath:     "/opt/skywire/users.db",
			EnableAuth: true,
		},
	}
	return pkgConfig
}

// UserConfig contains installation paths (for linux)
func UserConfig() PkgConfig {
	usrConfig := PkgConfig{
		Launcher: Launcher{
			BinPath: "/opt/skywire/apps",
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
	if distro.IsArmbian() || distro.IsDebian() || distro.IsRaspbian() || distro.IsUbuntu() {
		//enabling install-skyire.service and rebooting is required to avoid interrupting an update when the running visor is stopped
		//install-skywire.service is provided by the skybian package and calls install-skywire.sh
		return []string{`systemctl enable install-skywire.service && systemctl reboot || echo -e "Resource unavailable.\nPlease update manually as specified here:\nhttps://github.com/skycoin/skywire/wiki/Skywire-Package-Installation"`}
	}
	return []string{`echo -e "Update not implemented for this linux distro.\nPlease update skywire the same way you installed it."`}
}

// Survey system hardware survey struct
type Survey struct {
	PubKey         cipher.PubKey    `json:"public_key,omitempty"`
	SkycoinAddress string           `json:"skycoin_address,omitempty"`
	GOOS           string           `json:"go_os,omitempty"`
	GOARCH         string           `json:"go_arch,omitempty"`
	SYSINFO        sysinfo.SysInfo  `json:"zcalusic_sysinfo,omitempty"`
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
	var si sysinfo.SysInfo
	si.GetSysInfo()
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
		IPAddr:         IPA(),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		SYSINFO:        si,
		UUID:           uuid.New(),
		Disks:          disks,
		Product:        product,
		Memory:         memory,
		SkywireVersion: Version(),
	}
	return s, nil
}
