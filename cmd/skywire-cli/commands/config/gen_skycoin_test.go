package cliconfig

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestBuildSkycoinDaemonApps_Legacy(t *testing.T) {
	resetSkycoinFlags(t)
	skycoinDaemonInstances = ""
	isSkycoinDaemonEnable = true
	skycoinDaemonFiber = "/etc/aix.toml"
	skycoinDaemonAPISets = "READ,STATUS"
	skycoinDaemonUser = "skycoin"

	apps, err := buildSkycoinDaemonApps()
	if err != nil {
		t.Fatalf("legacy path: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("legacy path emits 1 app, got %d", len(apps))
	}
	a := apps[0]
	if a.Name != "skycoin-daemon" {
		t.Fatalf("name = %q", a.Name)
	}
	if !a.AutoStart {
		t.Fatal("AutoStart should mirror SKYCOIND")
	}
	want := []string{"skycoin", "daemon",
		"--enable-all-api-sets=true", "--log-level=debug",
		"--enable-gui-api-sets", "READ,STATUS",
	}
	if !slicesEq(a.Args, want) {
		t.Fatalf("legacy args = %v, want %v", a.Args, want)
	}
	if len(a.Env) != 1 || a.Env[0] != "FIBER_TOML=/etc/aix.toml" {
		t.Fatalf("legacy env = %v", a.Env)
	}
	if a.User != "skycoin" {
		t.Fatalf("user = %q", a.User)
	}
}

func TestBuildSkycoinDaemonApps_Multi(t *testing.T) {
	resetSkycoinFlags(t)
	skycoinDaemonInstances = "skycoin,/etc/skywire/fibercoins/aix.toml,/etc/skywire/fibercoins/privateness.toml"
	skycoinDaemonFlags = "--enable-gui-api-sets READ,STATUS"
	skycoinDaemonUser = "skycoin"
	isSkycoinDaemonEnable = true
	conf = &visorconfig.V1{}
	conf.LocalPath = "/opt/skywire/local"

	apps, err := buildSkycoinDaemonApps()
	if err != nil {
		t.Fatalf("multi: %v", err)
	}
	if len(apps) != 3 {
		t.Fatalf("multi emits 3 apps, got %d", len(apps))
	}

	// 0: skycoin (built-in defaults, no FIBER_TOML)
	if apps[0].Name != "skycoin-daemon-skycoin" {
		t.Fatalf("[0].Name = %q", apps[0].Name)
	}
	if len(apps[0].Env) != 0 {
		t.Fatalf("[0] should have no env, got %v", apps[0].Env)
	}
	wantArgs0 := []string{"skycoin", "daemon",
		"--port", "6420",
		"--data-dir", "/opt/skywire/local/skycoin-daemon-skycoin",
		"--enable-all-api-sets=true", "--log-level=debug",
		"--enable-gui-api-sets", "READ,STATUS",
	}
	if !slicesEq(apps[0].Args, wantArgs0) {
		t.Fatalf("[0] args = %v, want %v", apps[0].Args, wantArgs0)
	}

	// 1: aix
	if apps[1].Name != "skycoin-daemon-aix" {
		t.Fatalf("[1].Name = %q", apps[1].Name)
	}
	if len(apps[1].Env) != 1 || apps[1].Env[0] != "FIBER_TOML=/etc/skywire/fibercoins/aix.toml" {
		t.Fatalf("[1] env = %v", apps[1].Env)
	}
	if !strings.Contains(strings.Join(apps[1].Args, " "), "--port 6422 --data-dir /opt/skywire/local/skycoin-daemon-aix") {
		t.Fatalf("[1] args = %v", apps[1].Args)
	}

	// 2: privateness
	if apps[2].Name != "skycoin-daemon-privateness" {
		t.Fatalf("[2].Name = %q", apps[2].Name)
	}
	if !strings.Contains(strings.Join(apps[2].Args, " "), "--port 6424") {
		t.Fatalf("[2] args = %v", apps[2].Args)
	}
}

func TestBuildSkycoinDaemonApps_FlagsConflict(t *testing.T) {
	resetSkycoinFlags(t)
	skycoinDaemonInstances = "skycoin"
	skycoinDaemonFlags = "--port 9999"
	conf = &visorconfig.V1{}

	_, err := buildSkycoinDaemonApps()
	if err == nil {
		t.Fatal("expected error when --skycoindflags carries --port")
	}
}

func TestBuildSkycoinDaemonApps_DuplicateName(t *testing.T) {
	resetSkycoinFlags(t)
	// Two paths whose basenames collide → same derived instance name.
	skycoinDaemonInstances = "/path/a/aix.toml,/path/b/aix.toml"
	conf = &visorconfig.V1{}

	_, err := buildSkycoinDaemonApps()
	if err == nil {
		t.Fatal("expected error on duplicate instance name")
	}
}

func resetSkycoinFlags(t *testing.T) {
	t.Helper()
	skycoinDaemonInstances = ""
	skycoinDaemonFlags = ""
	skycoinDaemonFiber = ""
	skycoinDaemonAPISets = ""
	skycoinDaemonUser = ""
	isSkycoinDaemonEnable = false
}

func slicesEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
