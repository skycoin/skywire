//go:build darwin
// +build darwin

package visorconfig

import (
	"net"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jaypipes/ghw"
	"github.com/zcalusic/sysinfo"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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
		SYSINFO:        genSysInfo(),
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
	Node    sysinfo.Node            `json:"node,omitempty"`
}

func genSysInfo() customSysinfo {
	var sysInfo customSysinfo
	sysInfo.Network = getMacAddr()
	sysInfo.Node.Hypervisor = getNodeHypervisor()
	return sysInfo
}

func getMacAddr() []sysinfo.NetworkDevice {
	si := make([]sysinfo.NetworkDevice, 1)
	interfaces, err := net.Interfaces()
	if err != nil {
		return si
	}
	for _, ifa := range interfaces {
		si[0].MACAddress = ifa.HardwareAddr.String()
		if si[0].MACAddress != "" {
			return si
		}
	}
	return si
}

func getNodeHypervisor() string {
	// Check docker
	// Check for the /.dockerenv file
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}
	// Check for cgroup indicating Docker or container environment
	data, err := os.ReadFile("/proc/self/cgroup")
	if err == nil && strings.Contains(string(data), "docker") {
		return "docker"
	}

	// Check other virtualization: kvm, xenhvm, virtualbox, vmware, qemu, hyperv
	dmiFiles := []string{
		"/sys/class/dmi/id/product_name",
		"/sys/class/dmi/id/sys_vendor",
	}

	for _, file := range dmiFiles {
		data, err := os.ReadFile(file) //nolint: gosec
		if err == nil {
			content := strings.ToLower(string(data))
			if strings.Contains(content, "kvm") {
				return "kvm"
			} else if strings.Contains(content, "xenhvm") {
				return "xenhvm"
			} else if strings.Contains(content, "virtualbox") {
				return "virtualbox"
			} else if strings.Contains(content, "vmware") {
				return "vmware"
			} else if strings.Contains(content, "qemu") {
				return "qemu"
			} else if strings.Contains(content, "hyperv") {
				return "hyperv"
			}
		}
	}

	// no virtual
	return ""
}
