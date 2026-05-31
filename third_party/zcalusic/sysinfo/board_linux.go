// Copyright © 2016 Zlatko Čalušić
//
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.

package sysinfo

// (Board type is declared in board.go so the survey JSON shape is
// available to non-Linux build targets that just need the struct for
// marshaling.)

func (si *SysInfo) getBoardInfo() {
	si.Board.Name = slurpFile("/sys/class/dmi/id/board_name")
	si.Board.Vendor = slurpFile("/sys/class/dmi/id/board_vendor")
	si.Board.Version = slurpFile("/sys/class/dmi/id/board_version")
	si.Board.Serial = slurpFile("/sys/class/dmi/id/board_serial")
	si.Board.AssetTag = slurpFile("/sys/class/dmi/id/board_asset_tag")

	// ARM SBC fallback: /sys/class/dmi/id/* only exists on systems with
	// DMI/SMBIOS (essentially x86/x86_64). On ARM SBCs (Raspberry Pi,
	// Pine64, Rock Pi, NanoPi…) the equivalent identification comes
	// from device-tree at /sys/firmware/devicetree/base/. Populate
	// Board.Name from there when DMI didn't yield one. Vendor stays
	// empty rather than guessing — the device-tree "model" string
	// often embeds the vendor inline (e.g. "Raspberry Pi 4 Model B Rev
	// 1.4") and parsing it out would be brittle.
	if si.Board.Name == "" {
		si.Board.Name = readDeviceTreeModel()
	}
}
