// Copyright © 2016 Zlatko Čalušić
//
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.

package sysinfo

// CPU information.
type CPU struct {
	Vendor  string `json:"vendor,omitempty"`
	Model   string `json:"model,omitempty"`
	Speed   uint   `json:"speed,omitempty"`   // CPU clock rate in MHz
	Cache   uint   `json:"cache,omitempty"`   // CPU cache size in KB
	Cpus    uint   `json:"cpus,omitempty"`    // number of physical CPUs
	Cores   uint   `json:"cores,omitempty"`   // number of physical CPU cores
	Threads uint   `json:"threads,omitempty"` // number of logical (HT) CPU cores
}
