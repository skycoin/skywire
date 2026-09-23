// Package cliconfig cmd/skywire-cli/commands/config/skyenvdefaults_test.go c4-vis-cli
package cliconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
)

// TestRefreshSkyenvDefaults registers flags while the env file is
// absent — the state the browser build's `config gen` registers in —
// then points skyenvfile at a file that sets the variables and checks
// that the refresh picks them up without overriding what the caller set
// on the command line.
func TestRefreshSkyenvDefaults(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "skywire.conf")

	saved := skyenvfile
	t.Cleanup(func() { skyenvfile = saved })
	skyenvfile = filepath.Join(dir, "absent.conf")

	var (
		wisp     bool
		explicit bool
		addr     string
		port     int
	)
	fs := pflag.NewFlagSet("gen", pflag.ContinueOnError)
	t.Cleanup(func() { delete(skyenvFlagDefaults, fs) })

	skyenvBoolVar(fs, &wisp, "wisp", "${WISP:-false}", "")
	skyenvBoolVar(fs, &explicit, "explicit", "${EXPLICIT:-false}", "")
	skyenvStringVar(fs, &addr, "chataddr", "${SKYCHATADDR:-:8001}", "")
	skyenvIntVar(fs, &port, "wispport", "${WISPPORT:-0}", "")

	if wisp || explicit || addr != ":8001" || port != 0 {
		t.Fatalf("registration defaults with no env file = %v %v %q %d", wisp, explicit, addr, port)
	}

	// The caller set --explicit=false explicitly; the env file says true.
	if err := fs.Set("explicit", "false"); err != nil {
		t.Fatalf("Set explicit: %v", err)
	}

	env := "WISP=true\nEXPLICIT=true\nSKYCHATADDR=:9001\nWISPPORT=6001\n"
	if err := os.WriteFile(conf, []byte(env), 0600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	skyenvfile = conf

	refreshSkyenvDefaults(fs)

	if !wisp {
		t.Errorf("wisp = false, want true (env file sets WISP=true)")
	}
	if explicit {
		t.Errorf("explicit = true, want false: an explicitly set flag must not be overridden")
	}
	if addr != ":9001" {
		t.Errorf("chataddr = %q, want %q", addr, ":9001")
	}
	if port != 6001 {
		t.Errorf("wispport = %d, want 6001", port)
	}
	if f := fs.Lookup("wisp"); f == nil || f.Value.String() != "true" {
		t.Errorf("flag set view of wisp = %v, want true", f)
	}
	if f := fs.Lookup("wisp"); f != nil && f.Changed {
		t.Errorf("refresh marked wisp as Changed; a refreshed default is still a default")
	}
	if initVal, runVal := skyenvDefaultValues(fs, "wisp"); initVal != "false" || runVal != "true" {
		t.Errorf("skyenvDefaultValues(wisp) = %q, %q; want \"false\", \"true\"", initVal, runVal)
	}
}
