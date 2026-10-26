package clirewards

import (
	"encoding/json"
	"testing"
)

func TestExtractInterfacesAndroid(t *testing.T) {
	survey := func(js string) *surveyData {
		var s surveyData
		if err := json.Unmarshal([]byte(js), &s); err != nil {
			t.Fatal(err)
		}
		return &s
	}

	// Android: keyed on the machine id, never the fake MAC apps see.
	s := survey(`{"go_os":"android","zcalusic_sysinfo":{"node":{"machineid":"abc123"},"network":[{"name":"wlan0","macaddress":"02:00:00:00:00:00"}]}}`)
	ifc, macs := extractInterfaces(s)
	if macs[0] != "android:abc123" || ifc == "" {
		t.Fatalf("android with machine id: ifc=%q macs=%v", ifc, macs)
	}

	// Android without a machine id is ineligible, as a linux survey without a MAC is.
	s = survey(`{"go_os":"android","zcalusic_sysinfo":{"network":[{"name":"wlan0","macaddress":"02:00:00:00:00:00"}]}}`)
	if ifc, macs := extractInterfaces(s); ifc != "" || macs[0] != "" {
		t.Fatalf("android without machine id: ifc=%q macs=%v", ifc, macs)
	}

	// Linux keeps keying on the first MAC.
	s = survey(`{"go_os":"linux","zcalusic_sysinfo":{"node":{"machineid":"abc123"},"network":[{"name":"eth0","macaddress":"aa:bb:cc:dd:ee:ff"}]}}`)
	if _, macs := extractInterfaces(s); macs[0] != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("linux: macs=%v", macs)
	}
}
