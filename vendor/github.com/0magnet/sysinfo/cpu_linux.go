// Copyright © 2016 Zlatko Čalušić
//
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.
//
// Internal skywire fork: parsing extended for ARM /proc/cpuinfo formats
// (CPU implementer / CPU part / Hardware fields) and for device-tree
// model so ARM SBCs no longer report null CPU and Board info.

package sysinfo

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// (CPU type is declared in cpu.go so the survey JSON shape is available
// to non-Linux build targets that just need the struct for marshaling.)

var (
	reTwoColumns = regexp.MustCompile("\t+: ")
	reExtraSpace = regexp.MustCompile(" +")
	reCacheSize  = regexp.MustCompile(`^(\d+) KB$`)
)

// armImplementers maps the ARM "CPU implementer" byte (hex string as
// written in /proc/cpuinfo) to a human-readable vendor name. Source:
// Linux kernel arch/arm64/kernel/cpuinfo.c and arch/arm/include/asm/cputype.h.
var armImplementers = map[string]string{
	"0x41": "ARM",
	"0x42": "Broadcom",
	"0x43": "Cavium",
	"0x44": "DEC",
	"0x46": "Fujitsu",
	"0x48": "HiSilicon",
	"0x49": "Infineon",
	"0x4d": "Motorola/Freescale",
	"0x4e": "NVIDIA",
	"0x50": "APM",
	"0x51": "Qualcomm",
	"0x53": "Samsung",
	"0x56": "Marvell",
	"0x61": "Apple",
	"0x66": "Faraday",
	"0x69": "Intel",
	"0x70": "Phytium",
	"0xc0": "Ampere",
}

// armPartsByImplementer maps (implementer, part) → Cortex / SoC core name.
// Only ARM-Ltd parts (implementer 0x41) are an exhaustive list; other
// implementers contribute a few well-known parts that appear on SBCs we
// see in the wild.
var armPartsByImplementer = map[string]map[string]string{
	"0x41": { // ARM Ltd cores
		"0x920": "ARM920", "0x926": "ARM926", "0x946": "ARM946",
		"0x966": "ARM966", "0xa20": "ARM1020", "0xa22": "ARM1022",
		"0xa26": "ARM1026", "0xb02": "ARM11 MPCore", "0xb36": "ARM1136",
		"0xb56": "ARM1156", "0xb76": "ARM1176",
		"0xc05": "Cortex-A5", "0xc07": "Cortex-A7", "0xc08": "Cortex-A8",
		"0xc09": "Cortex-A9", "0xc0d": "Cortex-A12", "0xc0e": "Cortex-A17",
		"0xc0f": "Cortex-A15",
		"0xc14": "Cortex-R4", "0xc15": "Cortex-R5",
		"0xc17": "Cortex-R7", "0xc18": "Cortex-R8",
		"0xc20": "Cortex-M0", "0xc21": "Cortex-M1", "0xc23": "Cortex-M3",
		"0xc24": "Cortex-M4", "0xc27": "Cortex-M7", "0xc60": "Cortex-M0+",
		"0xd01": "Cortex-A32", "0xd02": "Cortex-A34", "0xd03": "Cortex-A53",
		"0xd04": "Cortex-A35", "0xd05": "Cortex-A55", "0xd06": "Cortex-A65",
		"0xd07": "Cortex-A57", "0xd08": "Cortex-A72", "0xd09": "Cortex-A73",
		"0xd0a": "Cortex-A75", "0xd0b": "Cortex-A76", "0xd0c": "Neoverse-N1",
		"0xd0d": "Cortex-A77", "0xd0e": "Cortex-A76AE",
		"0xd13": "Cortex-R52", "0xd15": "Cortex-R82",
		"0xd20": "Cortex-A78", "0xd23": "Cortex-A78C",
		"0xd40": "Neoverse-V1", "0xd41": "Cortex-A78",
		"0xd44": "Cortex-X1", "0xd46": "Cortex-A510",
		"0xd47": "Cortex-A710", "0xd48": "Cortex-X2",
		"0xd49": "Neoverse-N2", "0xd4a": "Neoverse-E1",
		"0xd4b": "Cortex-A78C",
	},
	"0x42": { // Broadcom
		"0x100": "Brahma-B53",
		"0x516": "ThunderX2",
	},
	"0x43": { // Cavium
		"0x0a0": "ThunderX", "0x0a1": "ThunderX-88XX",
		"0x0a2": "ThunderX-81XX", "0x0a3": "ThunderX-83XX",
		"0x0af": "ThunderX2-99xx",
	},
	"0x4e": { // NVIDIA
		"0x000": "Denver", "0x003": "Denver2", "0x004": "Carmel",
	},
	"0x51": { // Qualcomm
		"0x00f": "Scorpion", "0x02d": "Scorpion", "0x04d": "Krait",
		"0x06f": "Krait", "0x201": "Kryo", "0x205": "Kryo",
		"0x211": "Kryo", "0x800": "Falkor-V1/Kryo", "0x801": "Kryo-V2",
		"0x803": "Kryo-3XX-Silver", "0x804": "Kryo-4XX-Gold",
		"0x805": "Kryo-4XX-Silver", "0xc00": "Falkor", "0xc01": "Saphira",
	},
	"0x53": { // Samsung
		"0x001": "Exynos-M1",
	},
	"0x56": { // Marvell
		"0x131": "Feroceon-88FR131", "0x581": "PJ4/PJ4b",
		"0x584": "PJ4B-MP",
	},
	"0x61": { // Apple
		"0x000": "Apple-A7", "0x001": "Apple-A8", "0x002": "Apple-A8X",
		"0x003": "Apple-A9", "0x004": "Apple-A9X", "0x005": "Apple-A10",
		"0x006": "Apple-A10X", "0x007": "Apple-A11", "0x008": "Apple-A12",
		"0x009": "Apple-A12X", "0x00b": "Apple-A13", "0x00c": "Apple-A14",
		"0x00d": "Apple-M1", "0x010": "Apple-M2",
	},
}

func (si *SysInfo) getCPUInfo() {
	n := runtime.NumCPU()
	if n > 0 {
		si.CPU.Threads = uint(n)
	}

	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return
	}
	defer f.Close()             //nolint:errcheck // read-only handle on /proc pseudo-file
	_ = parseProcCPUInfo(f, si) //nolint:errcheck // partial /proc/cpuinfo parse still yields usable fields
}

// parseProcCPUInfo is the testable core of getCPUInfo: it scans a
// /proc/cpuinfo-shaped stream and fills si.CPU. Extracted so we can
// drive ARM/x86 fixtures from a unit test without writing to /proc.
func parseProcCPUInfo(r io.Reader, si *SysInfo) error {
	cpu := make(map[string]bool)
	core := make(map[string]bool)

	var cpuID string

	// ARM-only carry-through state for after the main scan. The
	// implementer + part bytes appear once per logical core, but we
	// only need to consume them once to derive vendor/model when the
	// x86-style fields didn't fill them.
	var armImplementer, armPart string

	s := bufio.NewScanner(r)
	for s.Scan() {
		if sl := reTwoColumns.Split(s.Text(), 2); sl != nil {
			switch sl[0] {
			case "physical id":
				cpuID = sl[1]
				cpu[cpuID] = true
			case "core id":
				coreID := fmt.Sprintf("%s/%s", cpuID, sl[1])
				core[coreID] = true
			case "vendor_id":
				if si.CPU.Vendor == "" {
					si.CPU.Vendor = sl[1]
				}
			case "model name":
				if si.CPU.Model == "" {
					// CPU model, as reported by /proc/cpuinfo, can be a bit ugly. Clean up...
					model := reExtraSpace.ReplaceAllLiteralString(sl[1], " ")
					si.CPU.Model = strings.Replace(model, "- ", "-", 1)
				}
			case "cache size":
				if si.CPU.Cache == 0 {
					if m := reCacheSize.FindStringSubmatch(sl[1]); m != nil {
						if cache, err := strconv.ParseUint(m[1], 10, 64); err == nil {
							si.CPU.Cache = uint(cache)
						}
					}
				}
			case "CPU implementer":
				if armImplementer == "" {
					armImplementer = strings.ToLower(strings.TrimSpace(sl[1]))
				}
			case "CPU part":
				if armPart == "" {
					armPart = strings.ToLower(strings.TrimSpace(sl[1]))
				}
			}
		}
	}
	if err := s.Err(); err != nil {
		return err
	}

	si.CPU.Cpus = uint(len(cpu))
	si.CPU.Cores = uint(len(core))

	// ARM fallback. /proc/cpuinfo on aarch64 (and many arm/armv7
	// kernels) doesn't expose vendor_id or model name at all — instead
	// it publishes the integer "CPU implementer" / "CPU part" codes.
	// Translate those into vendor + Cortex-A name so the survey isn't
	// just {threads: N}.
	if si.CPU.Vendor == "" && armImplementer != "" {
		if v, ok := armImplementers[armImplementer]; ok {
			si.CPU.Vendor = v
		}
	}
	if si.CPU.Model == "" && armImplementer != "" && armPart != "" {
		if parts, ok := armPartsByImplementer[armImplementer]; ok {
			if part, ok := parts[armPart]; ok {
				si.CPU.Model = part
			}
		}
	}
	// If we still don't have a model but the implementer was recognized,
	// surface a generic label including the part code so a future SoC
	// not yet in our map still produces a non-empty field instead of
	// dropping silently.
	if si.CPU.Model == "" && si.CPU.Vendor != "" && armPart != "" {
		si.CPU.Model = fmt.Sprintf("%s CPU (part=%s)", si.CPU.Vendor, armPart)
	}
	return nil
}

// readDeviceTreeModel returns the first NUL-terminated string in
// /sys/firmware/devicetree/base/model. That file is present on most
// arm/arm64 SBCs (Raspberry Pi, Pine64, Rock Pi, NanoPi, BananaPi …)
// and contains the human-readable board description, e.g.
// "Raspberry Pi 4 Model B Rev 1.4". Returns empty on x86 / non-DT
// systems where the file is absent.
func readDeviceTreeModel() string {
	data, err := os.ReadFile("/sys/firmware/devicetree/base/model")
	if err != nil {
		return ""
	}
	// device-tree strings are NUL-terminated; the trailing NUL is part
	// of the file. Trim it (and any whitespace) before returning.
	if idx := bytes.IndexByte(data, 0); idx >= 0 {
		data = data[:idx]
	}
	return strings.TrimSpace(string(data))
}
