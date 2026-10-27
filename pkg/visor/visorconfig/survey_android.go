//go:build android

// Package visorconfig pkg/visor/visorconfig/survey_android.go c3-vis-core
package visorconfig

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zcalusic/sysinfo"
)

// AndroidDeviceEnv names the environment variable the Android app passes the
// device's identity and build properties in, as JSON (see androidDevice).
const AndroidDeviceEnv = "SKYWIRE_ANDROID_DEVICE"

// androidDevice is what the app knows about the device and the core cannot
// read for itself: android.os.Build and Settings.Secure.ANDROID_ID.
type androidDevice struct {
	AndroidID       string `json:"android_id"`
	Manufacturer    string `json:"manufacturer"`
	Brand           string `json:"brand"`
	Model           string `json:"model"`
	Device          string `json:"device"`
	Product         string `json:"product"`
	Board           string `json:"board"`
	Hardware        string `json:"hardware"`
	Fingerprint     string `json:"fingerprint"`
	SocManufacturer string `json:"soc_manufacturer"`
	SocModel        string `json:"soc_model"`
	Release         string `json:"release"`
	SDK             int    `json:"sdk"`
}

// SystemSurvey returns the system survey on Android. Most of what sysinfo
// reads on linux (DMI, machine-id, real MACs) is absent or hidden from apps,
// so the device's identity and build come from the app instead. The fake MAC
// apps see is dropped; the reward side keys an Android survey on its machine
// id.
func SystemSurvey() (Survey, error) {
	var si sysinfo.SysInfo
	si.GetSysInfo()

	var d androidDevice
	if v := os.Getenv(AndroidDeviceEnv); v != "" {
		_ = json.Unmarshal([]byte(v), &d) //nolint:errcheck
	}

	if d.AndroidID != "" {
		sum := sha256.Sum256([]byte("skywire-android-machine-id:" + d.AndroidID))
		si.Node.MachineID = hex.EncodeToString(sum[:16])
	}
	if si.Node.Hypervisor == "" && androidEmulator(d) {
		si.Node.Hypervisor = "android-emulator"
	}

	si.OS = sysinfo.OS{
		Name:         strings.TrimSpace("Android " + d.Release),
		Vendor:       "android",
		Version:      d.Release,
		Release:      strconv.Itoa(d.SDK),
		Architecture: si.Kernel.Architecture,
	}
	si.Product = sysinfo.Product{Name: d.Model, Vendor: d.Manufacturer, Version: d.Device}
	si.Board = sysinfo.Board{Name: d.Board, Vendor: d.Brand, Version: d.Hardware}
	if si.Kernel.Release == "" {
		var u syscall.Utsname
		if syscall.Uname(&u) == nil {
			si.Kernel.Release = utsString(u.Release[:])
			si.Kernel.Version = utsString(u.Version[:])
		}
	}
	if d.SocModel != "" {
		si.CPU.Model = strings.TrimSpace(d.SocManufacturer + " " + d.SocModel)
	}
	if si.CPU.Threads == 0 {
		si.CPU.Threads = uint(runtime.NumCPU())
	}
	if si.Memory.Size == 0 {
		si.Memory.Size = memTotalMB()
	}
	if len(si.Storage) == 0 {
		if gb := dataStorageGB(); gb > 0 {
			si.Storage = []sysinfo.StorageDevice{{Name: "data", Size: gb}}
		}
	}
	nets := si.Network[:0]
	for _, n := range si.Network {
		if n.MACAddress != "" && n.MACAddress != "02:00:00:00:00:00" {
			nets = append(nets, n)
		}
	}
	si.Network = nets

	return Survey{
		Timestamp:      time.Now(),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		SYSINFO:        si,
		UUID:           uuid.New(),
		SkywireVersion: Version(),
	}, nil
}

// androidEmulator reports the signs of the Android emulator and the common
// x86 ones. It is a heuristic, like the hypervisor check on linux: a patched
// emulator can hide them.
func androidEmulator(d androidDevice) bool {
	switch strings.ToLower(d.Hardware) {
	case "goldfish", "ranchu", "vbox86", "ttvm_x86":
		return true
	}
	fp := strings.ToLower(d.Fingerprint)
	if strings.HasPrefix(fp, "generic") || strings.Contains(fp, "/sdk_gphone") || strings.Contains(fp, "emulator") {
		return true
	}
	if strings.Contains(d.Model, "Android SDK built for") || strings.Contains(strings.ToLower(d.Model), "emulator") {
		return true
	}
	for _, f := range []string{"/dev/qemu_pipe", "/dev/goldfish_pipe", "/dev/socket/qemud"} {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}

// memTotalMB reads MemTotal from /proc/meminfo, which apps may read.
func memTotalMB() uint {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close() //nolint:errcheck
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, _ := strconv.ParseUint(fields[1], 10, 64) //nolint:errcheck
			return uint(kb / 1024)
		}
	}
	return 0
}

// dataStorageGB is the size of the filesystem holding the app's data.
func dataStorageGB() uint {
	dir, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0
	}
	return uint(st.Blocks * uint64(st.Bsize) / 1e9) //nolint:gosec // a block size is never negative
}

func utsString(b []int8) string {
	s := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		s = append(s, byte(c)) //nolint:gosec // utsname holds bytes
	}
	return string(s)
}
