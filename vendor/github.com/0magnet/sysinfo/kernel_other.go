//go:build !linux
// +build !linux

// Copyright © 2016 Zlatko Čalušić
//
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.

package sysinfo

// Kernel information.
type Kernel struct {
	Release      string `json:"release,omitempty"`
	Version      string `json:"version,omitempty"`
	Architecture string `json:"architecture,omitempty"`
}

// getKernelInfo is a no-op on non-Linux targets that import this
// package only for the SysInfo / Kernel type shape used in survey
// marshaling. The Linux build (kernel_linux.go) reads
// /proc/sys/kernel and syscall.Uname.
func (si *SysInfo) getKernelInfo() {}
