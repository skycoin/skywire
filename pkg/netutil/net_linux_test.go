//go:build linux

package netutil

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDefaultRouteInterfaces(t *testing.T) {
	const table = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"wlan0\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n" +
		"tun0\t00000000\t00000000\t0000\t0\t0\t50\t00000000\t0\t0\t0\n" + // down
		"eth0\t00000000\t0201A8C0\t0003\t0\t0\t700\t00000000\t0\t0\t0\n" // a second route, same interface
	got := strings.Join(defaultRouteInterfaces(strings.NewReader(table)), ",")
	if got != "eth0,wlan0" {
		t.Fatalf("default route interfaces %q, want eth0 then wlan0, lowest metric first", got)
	}
	if got := defaultRouteInterfaces(strings.NewReader("Iface\tDestination\n")); len(got) != 0 {
		t.Errorf("a table with no default route gave %q", got)
	}
}

// TestDefaultNetworkInterfaceMatchesIPRoute: where the ip tool is installed,
// the answer is the one `ip r` gives, which this replaces.
func TestDefaultNetworkInterfaceMatchesIPRoute(t *testing.T) {
	out, err := exec.Command("sh", "-c", `ip r | awk '$1 == "default" {print $5}'`).Output()
	if err != nil || len(out) == 0 {
		t.Skip("no ip tool or no default route here")
	}
	got, err := DefaultNetworkInterface()
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("DefaultNetworkInterface %q, ip r %q", got, want)
	}
}
