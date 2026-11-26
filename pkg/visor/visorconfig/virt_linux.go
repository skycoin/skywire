//go:build linux && !android

// Package visorconfig pkg/visor/visorconfig/virt_linux.go c3-vis-core
package visorconfig

import (
	"os"
	"path/filepath"
	"strings"
)

// vmDMIPrefixes maps a DMI string prefix seen inside a VM to the VM's name. It
// follows systemd's detect_vm_dmi_vendor table, src/basic/virt.c.
var vmDMIPrefixes = []struct{ prefix, name string }{
	{"kvm", "kvm"},
	{"openstack", "kvm"},
	{"kubevirt", "kvm"},
	{"amazon ec2", "amazon"},
	{"qemu", "qemu"},
	{"vmware", "vmware"},
	{"vmw", "vmware"},
	{"innotek gmbh", "oracle"},
	{"virtualbox", "oracle"},
	{"xen", "xen"},
	{"bochs", "bochs"},
	{"parallels", "parallels"},
	{"bhyve", "bhyve"},
	{"apple virtualization", "apple"},
	{"google compute engine", "google"},
	{"alibaba cloud ecs", "alibaba"},
	{"droplet", "digitalocean"},
	{"vserver", "hetzner"},
}

// vmDMIFiles are the DMI strings systemd checks, readable without root.
var vmDMIFiles = []string{"product_name", "sys_vendor", "board_vendor", "bios_vendor", "product_version"}

// vmDetect names the VM a linux host runs in from DMI and the device tree,
// which also cover arm, where the CPUID check sysinfo uses never fires.
func vmDetect(root string) string {
	dmi := filepath.Join(root, "sys/class/dmi/id")
	if strings.HasPrefix(fileLower(filepath.Join(dmi, "sys_vendor")), "microsoft") &&
		fileLower(filepath.Join(dmi, "product_name")) == "virtual machine" {
		return "microsoft"
	}
	for _, f := range vmDMIFiles {
		v := fileLower(filepath.Join(dmi, f))
		for _, p := range vmDMIPrefixes {
			if v != "" && strings.HasPrefix(v, p.prefix) {
				return p.name
			}
		}
	}
	// Device tree strings are NUL separated. See systemd detect_vm_device_tree.
	hv := fileLower(filepath.Join(root, "proc/device-tree/hypervisor/compatible"))
	switch {
	case strings.Contains(hv, "linux,kvm"):
		return "kvm"
	case strings.Contains(hv, "xen"):
		return "xen"
	case strings.Contains(hv, "vmware"):
		return "vmware"
	}
	if strings.Contains(fileLower(filepath.Join(root, "proc/device-tree/compatible")), "linux,dummy-virt") {
		return "qemu"
	}
	return ""
}

// containerDetect names the container a linux visor runs in, following
// systemd's detect_container, src/basic/virt.c.
func containerDetect(root string) string {
	if fileExists(filepath.Join(root, "proc/vz")) && !fileExists(filepath.Join(root, "proc/bc")) {
		return "openvz"
	}
	if fileExists(filepath.Join(root, "run/.containerenv")) {
		return "podman"
	}
	if fileExists(filepath.Join(root, ".dockerenv")) {
		return "docker"
	}
	if v := fileLower(filepath.Join(root, "run/systemd/container")); v != "" {
		return v
	}
	if env, err := os.ReadFile(filepath.Join(root, "proc/1/environ")); err == nil { //nolint:gosec
		for _, kv := range strings.Split(string(env), "\x00") {
			if v, ok := strings.CutPrefix(kv, "container="); ok && v != "" {
				return strings.ToLower(v)
			}
		}
	}
	cgroup := fileLower(filepath.Join(root, "proc/1/cgroup"))
	for _, marker := range []string{"docker", "lxc", "kubepods", "containerd", "libpod"} {
		if strings.Contains(cgroup, marker) {
			return marker
		}
	}
	return ""
}

// bootIDRead returns the kernel's boot id. Containers on one host share the
// host kernel, so they all report the same one.
func bootIDRead(root string) string {
	return fileLower(filepath.Join(root, "proc/sys/kernel/random/boot_id"))
}

// fileLower is a small file's content, lowercased and trimmed, or "" if unreadable.
func fileLower(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.Trim(string(b), " \t\r\n\x00"))
}

// fileExists reports whether path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
