//go:build !linux
// +build !linux

// Copyright © 2016 Zlatko Čalušić
//
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.

package sysinfo

// getBoardInfo is a no-op on non-Linux targets; /sys/class/dmi/id and
// /sys/firmware/devicetree are Linux-specific.
func (si *SysInfo) getBoardInfo() {}
