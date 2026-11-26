// Package clirewards cmd/skywire-cli/commands/rewards/calc_machine_test.go c4-vis-cli
package clirewards

import (
	"testing"
)

// machineSurvey is a survey of a linux visor with the given boot id.
func machineSurvey(bootID string) *surveyData {
	return &surveyData{GOOS: "linux", BootID: bootID}
}

// androidSurvey is a survey of an Android visor on a phone of the given model.
func androidSurvey(model string) *surveyData {
	s := &surveyData{GOOS: "android"}
	s.SysInfo.Product.Name = model
	s.SysInfo.Memory.Size = 7800
	s.SysInfo.Storage = append(s.SysInfo.Storage, struct {
		Size uint `json:"size"`
	}{Size: 238})
	return s
}

func TestMachineGroups(t *testing.T) {
	nodes := []nodeinfo{
		{PK: "host", MachineKeys: machineKeys(machineSurvey("boot-1"), "1.1.1.1", "aa")},
		{PK: "container-1", MachineKeys: machineKeys(machineSurvey("boot-1"), "1.1.1.1", "bb")},
		{PK: "container-2", MachineKeys: machineKeys(machineSurvey("boot-1"), "1.1.1.1", "cc")},
		{PK: "other-box", MachineKeys: machineKeys(machineSurvey("boot-2"), "1.1.1.1", "dd")},
		{PK: "old-visor-same-mac", MachineKeys: machineKeys(machineSurvey(""), "1.1.1.1", "dd")},
		{PK: "phone-app", MachineKeys: machineKeys(androidSurvey("Pixel 8"), "2.2.2.2", "android:id1")},
		{PK: "phone-clone", MachineKeys: machineKeys(androidSurvey("Pixel 8"), "2.2.2.2", "android:id2")},
		{PK: "phone-elsewhere", MachineKeys: machineKeys(androidSurvey("Pixel 8"), "3.3.3.3", "android:id3")},
		{PK: "other-phone", MachineKeys: machineKeys(androidSurvey("Galaxy S24"), "2.2.2.2", "android:id4")},
	}
	got := map[string]string{}
	for i, id := range machineGroups(nodes) {
		got[nodes[i].PK] = id
	}
	want := map[string]string{
		"host": "host", "container-1": "host", "container-2": "host",
		"other-box": "other-box", "old-visor-same-mac": "other-box",
		"phone-app": "phone-app", "phone-clone": "phone-app",
		"phone-elsewhere": "phone-elsewhere", "other-phone": "other-phone",
	}
	for pk, w := range want {
		if got[pk] != w {
			t.Errorf("%s: machine %q, want %q", pk, got[pk], w)
		}
	}
}

// Containers on one host split one share, as visors sharing a MAC always did.
func TestContainersOnOneHostShareOneShare(t *testing.T) {
	nodes := []nodeinfo{
		{PK: "a", IPAddr: "1.1.1.1", MachineKeys: machineKeys(machineSurvey("boot-1"), "1.1.1.1", "aa")},
		{PK: "b", IPAddr: "1.1.1.1", MachineKeys: machineKeys(machineSurvey("boot-1"), "1.1.1.1", "bb")},
		{PK: "c", IPAddr: "1.1.1.1", MachineKeys: machineKeys(machineSurvey("boot-1"), "1.1.1.1", "cc")},
		{PK: "d", IPAddr: "1.1.1.1", MachineKeys: machineKeys(machineSurvey("boot-2"), "1.1.1.1", "dd")},
	}
	machines := make([]string, len(nodes))
	for i, id := range machineGroups(nodes) {
		nodes[i].Machine = id
		machines[i] = id
	}
	ips := map[string]int{"1.1.1.1": len(nodes)}
	machineCounts := countFrequency(machines)
	var total float64
	for _, n := range nodes[:3] {
		total += calcPresenceShare(n, ips, machineCounts)
	}
	if !near(total, 1) {
		t.Fatalf("three containers on one host got %.4f shares, want 1", total)
	}
	if got := calcPresenceShare(nodes[3], ips, machineCounts); !near(got, 1) {
		t.Fatalf("the separate machine got %.4f, want 1", got)
	}
}
