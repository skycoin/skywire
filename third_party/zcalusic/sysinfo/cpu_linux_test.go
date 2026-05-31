//go:build linux
// +build linux

package sysinfo

import (
	"strings"
	"testing"
)

// procCPUInfoFixtures encodes representative /proc/cpuinfo outputs
// from the SBC + x86 mix this package's parser has to handle. The
// fields TAB-PADDED before the colon mirror what the kernel writes;
// reTwoColumns matches "\t+: " so we keep that exact shape.
var procCPUInfoFixtures = map[string]struct {
	body string

	wantVendor string
	wantModel  string
}{
	"x86_intel_core_i7_2600": {
		body: "processor\t: 0\n" +
			"vendor_id\t: GenuineIntel\n" +
			"model name\t: Intel(R) Core(TM) i7-2600 CPU @ 3.40GHz\n" +
			"cache size\t: 8192 KB\n" +
			"physical id\t: 0\n" +
			"core id\t\t: 0\n",
		wantVendor: "GenuineIntel",
		wantModel:  "Intel(R) Core(TM) i7-2600 CPU @ 3.40GHz",
	},

	"aarch64_raspberrypi4_cortex_a72": {
		body: "processor\t: 0\n" +
			"BogoMIPS\t: 108.00\n" +
			"Features\t: fp asimd evtstrm crc32 cpuid\n" +
			"CPU implementer\t: 0x41\n" +
			"CPU architecture: 8\n" +
			"CPU variant\t: 0x0\n" +
			"CPU part\t: 0xd08\n" +
			"CPU revision\t: 3\n",
		wantVendor: "ARM",
		wantModel:  "Cortex-A72",
	},

	"aarch64_pine64_cortex_a53": {
		body: "processor\t: 0\n" +
			"Features\t: fp asimd evtstrm aes pmull sha1 sha2 crc32\n" +
			"CPU implementer\t: 0x41\n" +
			"CPU architecture: 8\n" +
			"CPU variant\t: 0x0\n" +
			"CPU part\t: 0xd03\n" +
			"CPU revision\t: 4\n",
		wantVendor: "ARM",
		wantModel:  "Cortex-A53",
	},

	"armv7_raspberrypi_with_model_name": {
		body: "processor\t: 0\n" +
			"model name\t: ARMv7 Processor rev 5 (v7l)\n" +
			"BogoMIPS\t: 38.40\n" +
			"Features\t: half thumb fastmult vfp edsp neon vfpv3 tls vfpv4 idiva idivt vfpd32 lpae evtstrm crc32\n" +
			"CPU implementer\t: 0x41\n" +
			"CPU architecture: 7\n" +
			"CPU variant\t: 0x0\n" +
			"CPU part\t: 0xd03\n" +
			"CPU revision\t: 4\n",
		// model name present → kept verbatim; vendor still ARM via implementer.
		wantVendor: "ARM",
		wantModel:  "ARMv7 Processor rev 5 (v7l)",
	},

	"cavium_thunderx_aarch64": {
		body: "processor\t: 0\n" +
			"BogoMIPS\t: 200.00\n" +
			"Features\t: fp asimd evtstrm aes pmull sha1 sha2 crc32\n" +
			"CPU implementer\t: 0x43\n" +
			"CPU architecture: 8\n" +
			"CPU variant\t: 0x1\n" +
			"CPU part\t: 0x0a1\n" +
			"CPU revision\t: 1\n",
		wantVendor: "Cavium",
		wantModel:  "ThunderX-88XX",
	},

	"unknown_arm_implementer_falls_back_to_label": {
		body: "processor\t: 0\n" +
			"CPU implementer\t: 0x41\n" +
			"CPU architecture: 8\n" +
			"CPU part\t: 0xdef\n",
		wantVendor: "ARM",
		// 0xdef isn't in armPartsByImplementer for 0x41 → generic label.
		wantModel: "ARM CPU (part=0xdef)",
	},
}

func TestParseProcCPUInfo(t *testing.T) {
	for name, tc := range procCPUInfoFixtures {
		t.Run(name, func(t *testing.T) {
			var si SysInfo
			if err := parseProcCPUInfo(strings.NewReader(tc.body), &si); err != nil {
				t.Fatalf("parseProcCPUInfo: %v", err)
			}
			if si.CPU.Vendor != tc.wantVendor {
				t.Errorf("Vendor: got %q want %q", si.CPU.Vendor, tc.wantVendor)
			}
			if si.CPU.Model != tc.wantModel {
				t.Errorf("Model: got %q want %q", si.CPU.Model, tc.wantModel)
			}
		})
	}
}
