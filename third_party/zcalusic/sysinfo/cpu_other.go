//go:build !linux
// +build !linux

// Copyright © 2016 Zlatko Čalušić
//
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.

package sysinfo

import "runtime"

// getCPUInfo is a no-op on non-Linux targets that import this package
// (only for the SysInfo / CPU type shape used in survey marshaling).
// Linux is where /proc/cpuinfo lives; everywhere else we report the
// thread count from the Go runtime so the field isn't zero.
func (si *SysInfo) getCPUInfo() {
	si.CPU.Threads = uint(runtime.NumCPU()) //nolint:gosec // runtime.NumCPU() is always positive
}
