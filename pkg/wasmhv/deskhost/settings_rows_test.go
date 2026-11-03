package deskhost

import (
	"testing"

	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestSettingRowsDesk(t *testing.T) {
	rows := settingRows(visorapi.SkyenvState{Flags: autoconfigcmd.Describe()})
	locked := map[string]bool{}
	for _, r := range rows {
		locked[r.Key] = r.Locked
	}
	if !locked["ISHYPERVISOR"] || locked["MINDMSGSESS"] {
		t.Fatalf("locked = %v", locked)
	}
	if !hostOnly("VPNROUTERSSID") || hostOnly("MINDMSGSESS") {
		t.Fatal("hostOnly")
	}
}
