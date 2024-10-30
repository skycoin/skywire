//go:build windows
// +build windows

package visorconfig

import (
	"net"
	"runtime"

	"github.com/google/uuid"
	"github.com/jaypipes/ghw"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// UserConfig contains installation paths for running skywire as the user
func UserConfig() skyenv.PkgConfig {
	usrConfig := skyenv.PkgConfig{
		LauncherBinPath: "C:/Program Files/Skywire",
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
	SYSINFO        []NetworkDevice  `json:"zcalusic_sysinfo,omitempty"`
	IPAddr         string           `json:"ip_address,omitempty"`
	Disks          *ghw.BlockInfo   `json:"ghw_blockinfo,omitempty"`
	Product        *ghw.ProductInfo `json:"ghw_productinfo,omitempty"`
	Memory         *ghw.MemoryInfo  `json:"ghw_memoryinfo,omitempty"`
	UUID           uuid.UUID        `json:"uuid,omitempty"`
	SkywireVersion string           `json:"skywire_version,omitempty"`
	ServicesURLs   Services         `json:"services,omitempty"`
	DmsgServers    []string         `json:"dmsg_servers,omitempty"`
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
	//	var ipAddr string
	//	for {
	//		ipAddr, err = FetchIP(dmsgDisc)
	//		if err == nil {
	//			break
	//		}
	//	}

	s := Survey{
		//		IPAddr:         ipAddr,
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		SYSINFO:        getMacAddr(),
		UUID:           uuid.New(),
		Disks:          disks,
		Product:        product,
		Memory:         memory,
		SkywireVersion: Version(),
	}
	return s, nil
}

type NetworkDevice struct {
	Name       string `json:"name,omitempty"`
	Driver     string `json:"driver,omitempty"`
	MACAddress string `json:"macaddress,omitempty"`
	Port       string `json:"port,omitempty"`
	Speed      uint   `json:"speed,omitempty"` // device max supported speed in Mbps
}

func getMacAddr() []NetworkDevice {
	si := make([]NetworkDevice, 1)
	ifas, err := net.Interfaces()
	if err != nil {
		return nil
	}

	for _, ifa := range ifas {
		si[0].MACAddress = ifa.HardwareAddr.String()
		if si[0].MACAddress != "" {
			return si
		}
	}
	return nil
}
