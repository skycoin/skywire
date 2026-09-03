//go:build !linux
// +build !linux

// Copyright © 2016 Zlatko Čalušić
//
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.

package sysinfo

// NetworkDevice information.
type NetworkDevice struct {
	Name       string `json:"name,omitempty"`
	Driver     string `json:"driver,omitempty"`
	MACAddress string `json:"macaddress,omitempty"`
	Port       string `json:"port,omitempty"`
	Speed      uint   `json:"speed,omitempty"` // device max supported speed in Mbps
}

// getNetworkInfo is a no-op on non-Linux targets. The Linux build
// (network.go) enumerates /sys/class/net and queries ethtool via
// SIOCETHTOOL ioctl, both of which are Linux-specific.
func (si *SysInfo) getNetworkInfo() {}
