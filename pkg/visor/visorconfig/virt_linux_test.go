//go:build linux && !android

// Package visorconfig pkg/visor/visorconfig/virt_linux_test.go c3-vis-core
package visorconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// virtTree writes files, path to content, under a fresh root.
func virtTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestVMDetect(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"bare metal board":       {map[string]string{"sys/class/dmi/id/sys_vendor": "ASUSTeK COMPUTER INC.\n", "sys/class/dmi/id/product_name": "PN50\n"}, ""},
		"arm board without dmi":  {map[string]string{"proc/device-tree/compatible": "xunlong,orangepi-prime\x00allwinner,sun50i-h5\x00"}, ""},
		"surface is not hyper-v": {map[string]string{"sys/class/dmi/id/sys_vendor": "Microsoft Corporation\n", "sys/class/dmi/id/product_name": "Surface Pro 4\n"}, ""},
		"hyper-v":                {map[string]string{"sys/class/dmi/id/sys_vendor": "Microsoft Corporation\n", "sys/class/dmi/id/product_name": "Virtual Machine\n"}, "microsoft"},
		"arm cloud vm on kvm":    {map[string]string{"sys/class/dmi/id/sys_vendor": "QEMU\n", "sys/class/dmi/id/product_name": "KVM Virtual Machine\n"}, "kvm"},
		"ec2 graviton":           {map[string]string{"sys/class/dmi/id/sys_vendor": "Amazon EC2\n"}, "amazon"},
		"hetzner arm":            {map[string]string{"sys/class/dmi/id/sys_vendor": "Hetzner\n", "sys/class/dmi/id/product_name": "vServer\n"}, "hetzner"},
		"device tree kvm":        {map[string]string{"proc/device-tree/hypervisor/compatible": "linux,kvm\x00"}, "kvm"},
		"qemu virt machine":      {map[string]string{"proc/device-tree/compatible": "linux,dummy-virt\x00"}, "qemu"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := vmDetect(virtTree(t, c.files)); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestContainerDetect(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"host":         {map[string]string{"proc/1/cgroup": "0::/init.scope\n", "proc/1/environ": "HOME=/\x00TERM=linux\x00"}, ""},
		"docker":       {map[string]string{".dockerenv": ""}, "docker"},
		"podman":       {map[string]string{"run/.containerenv": ""}, "podman"},
		"nspawn":       {map[string]string{"run/systemd/container": "systemd-nspawn\n"}, "systemd-nspawn"},
		"lxc by env":   {map[string]string{"proc/1/environ": "HOME=/\x00container=lxc\x00"}, "lxc"},
		"cgroup v1":    {map[string]string{"proc/1/cgroup": "12:pids:/docker/3f2a\n"}, "docker"},
		"kubernetes":   {map[string]string{"proc/1/cgroup": "0::/kubepods/besteffort/pod1\n"}, "kubepods"},
		"openvz guest": {map[string]string{"proc/vz/veinfo": ""}, "openvz"},
		"openvz host":  {map[string]string{"proc/vz/veinfo": "", "proc/bc/0": ""}, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := containerDetect(virtTree(t, c.files)); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestVirtDetectThisHost(t *testing.T) {
	t.Logf("vm=%q container=%q boot_id=%q", vmDetect("/"), containerDetect("/"), bootIDRead("/"))
	if bootIDRead("/") == "" {
		t.Fatal("no boot id on a linux host")
	}
}
