//go:build windows
// +build windows

package visorconfig

import (
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jaypipes/ghw"
	"golang.org/x/sys/windows"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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
	Timestamp      time.Time        `json:"timestamp"`
	PubKey         cipher.PubKey    `json:"public_key,omitempty"`
	SkycoinAddress string           `json:"skycoin_address,omitempty"`
	GOOS           string           `json:"go_os,omitempty"`
	GOARCH         string           `json:"go_arch,omitempty"`
	SYSINFO        customSysinfo    `json:"zcalusic_sysinfo,omitempty"`
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

	s := Survey{
		Timestamp:      time.Now(),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		SYSINFO:        genSysInfo(),
		UUID:           uuid.New(),
		Disks:          disks,
		Product:        product,
		Memory:         memory,
		SkywireVersion: Version(),
	}
	return s, nil
}

type customSysinfo struct {
	Network []networkDevice `json:"network,omitempty"`
	Node    node            `json:"node,omitempty"`
}
type networkDevice struct {
	MACAddress string `json:"macaddress,omitempty"`
}

type node struct {
	Hypervisor string `json:"hypervisor,omitempty"`
}

func genSysInfo() customSysinfo {
	var sysInfo customSysinfo
	sysInfo.Network = getMacAddr()
	sysInfo.Node.Hypervisor = getNodeHypervisor()
	return sysInfo
}

func getMacAddr() []networkDevice {
	si := make([]networkDevice, 1)
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

// IsRoot checks for root permissions
func IsRoot() bool {
	var sid *windows.SID

	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid)
	if err != nil {
		log.Fatalf("SID Error: %s", err)
		return false
	}
	defer windows.FreeSid(sid) //nolint: errcheck

	token := windows.Token(0)

	member, err := token.IsMember(sid)
	if err != nil {
		log.Fatalf("Token Membership Error: %s", err)
		return false
	}

	return member
}
