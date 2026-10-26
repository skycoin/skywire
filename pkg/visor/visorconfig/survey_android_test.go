//go:build android

package visorconfig

import (
	"os"
	"testing"
)

func TestAndroidEmulator(t *testing.T) {
	phone := androidDevice{Manufacturer: "samsung", Model: "SM-S918B", Hardware: "qcom", Fingerprint: "samsung/dm3qxxx/dm3q:14/UP1A/S918BXXU3BWK1:user/release-keys"}
	for _, d := range []androidDevice{
		{Hardware: "ranchu"},
		{Hardware: "goldfish"},
		{Fingerprint: "google/sdk_gphone64_x86_64/emu64xa:16/BE2A/1:userdebug/dev-keys"},
		{Fingerprint: "generic/sdk/generic:8.0.0/OSR1/1:userdebug/test-keys"},
		{Model: "Android SDK built for x86"},
	} {
		if !androidEmulator(d) {
			t.Errorf("not flagged as an emulator: %+v", d)
		}
	}
	// The emulator's device files would flag any input, so only check a
	// phone's build fields where they are absent.
	if _, err := os.Stat("/dev/goldfish_pipe"); err != nil {
		if _, err := os.Stat("/dev/qemu_pipe"); err != nil && androidEmulator(phone) {
			t.Errorf("phone flagged as an emulator")
		}
	}
}

func TestAndroidSurvey(t *testing.T) {
	t.Setenv(AndroidDeviceEnv, `{"android_id":"1234abcd","manufacturer":"Google","model":"Pixel 8","hardware":"ranchu","release":"16","sdk":36}`)
	s, err := SystemSurvey()
	if err != nil {
		t.Fatal(err)
	}
	if s.SYSINFO.Node.MachineID == "" || s.SYSINFO.Node.Hypervisor == "" {
		t.Fatalf("machine id %q hypervisor %q", s.SYSINFO.Node.MachineID, s.SYSINFO.Node.Hypervisor)
	}
	if s.SYSINFO.Kernel.Release == "" || s.SYSINFO.Memory.Size == 0 || s.SYSINFO.OS.Name != "Android 16" {
		t.Fatalf("kernel %q memory %d os %q", s.SYSINFO.Kernel.Release, s.SYSINFO.Memory.Size, s.SYSINFO.OS.Name)
	}
	for _, n := range s.SYSINFO.Network {
		if n.MACAddress == "02:00:00:00:00:00" {
			t.Fatalf("fake MAC kept: %+v", n)
		}
	}
	t.Logf("machine id %s, hypervisor %s, kernel %s, memory %d MB", s.SYSINFO.Node.MachineID, s.SYSINFO.Node.Hypervisor, s.SYSINFO.Kernel.Release, s.SYSINFO.Memory.Size)
}
