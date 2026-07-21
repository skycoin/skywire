//go:build linux
// +build linux

// Package visorconfig pkg/visor/visorconfig/values_linux.go c3-vis-core
package visorconfig

import (
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jaypipes/ghw"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/third_party/zcalusic/sysinfo"
)

// UserConfig contains installation paths for running skywire as the user
func UserConfig() skyenv.PkgConfig {
	usrConfig := skyenv.PkgConfig{
		LauncherBinPath: "/opt/skywire/apps",
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
	SYSINFO        sysinfo.SysInfo  `json:"zcalusic_sysinfo,omitempty"`
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
	var si sysinfo.SysInfo
	si.GetSysInfo()
	disks, err := ghw.Block(ghw.WithDisableWarnings())
	if err != nil {
		return Survey{}, err
	}
	product, err := ghw.Product(ghw.WithDisableWarnings())
	if err != nil {
		return Survey{}, err
	}
	memory, err := ghw.Memory(ghw.WithDisableWarnings())
	if err != nil && !strings.Contains(err.Error(), "Could not determine total usable bytes of memory") {
		return Survey{}, err
	}

	s := Survey{
		Timestamp:      time.Now(),
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

// IsRoot checks for root permissions.
//
// It resolves the effective UID FIRST (0 == root), which never needs a passwd
// lookup. On a static (musl) build running under a UID with no /etc/passwd
// entry, user.Current() returns (nil, err); the previous code ignored the error
// and dereferenced the nil user, panicking in this package's init() — which
// crashed EVERY skywire command on such hosts (seen on the prod hv-serve unit,
// crash-looping v1.3.86). Geteuid is both safer and the canonical root test.
func IsRoot() bool {
	if os.Geteuid() == 0 {
		return true
	}
	// Non-zero euid: still honor a username match for the rare setup where
	// "root" is a non-zero uid, but guard the nil user.
	if u, err := user.Current(); err == nil && u != nil {
		return u.Username == "root"
	}
	return false
}
