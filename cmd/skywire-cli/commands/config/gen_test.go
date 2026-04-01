package cliconfig

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bitfield/script"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	shell string
)

func init() {
	switch runtime.GOOS {
	case "windows":
		if _, err := exec.LookPath("bash"); err == nil {
			shell = "bash"
		} else if _, err := exec.LookPath("powershell"); err == nil {
			shell = "powershell"
		} else {
			panic("Required binaries 'bash' and 'powershell' not found")
		}
	case "linux", "darwin":
		if _, err := exec.LookPath("bash"); err != nil {
			panic("Required binary 'bash' not found")
		}
		shell = "bash"
	default:
		panic("Unsupported operating system: " + runtime.GOOS)
	}
}

// Reference Issue https://github.com/skycoin/skywire/issues/1606

func TestConfigGenCmdFunc(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		expectedErr bool
	}{
		{
			name:        "first config gen -r",
			command:     "config gen -r -o test-config.json",
			expectedErr: false,
		},
		{
			name:        "second config gen -r",
			command:     "config gen -r -o test-config.json",
			expectedErr: false,
		},
		{
			name:        "config gen -rf",
			command:     "config gen -rf -o test-config.json",
			expectedErr: true,
		},
	}
	//nolint:errcheck
	_ = os.Remove("test-config.json")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := script.Exec(shell + ` -c "go run ../../skywire-cli.go ` + test.command + `"`).Stdout()
			if err != nil {
				if !test.expectedErr {
					t.Fatalf("Expected error: %v, but got: %v", test.expectedErr, err)
				}
			}
			if err == nil {
				if test.expectedErr {
					t.Fatalf("Expected error: %v, but got: %v", test.expectedErr, err)
				}
			}
		})
	}
	//nolint:errcheck
	_ = os.Remove("test-config.json")
}

// repoRoot returns the absolute path to the repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod)")
		}
		dir = parent
	}
}

// runConfigGen runs `go run . cli config gen -n` (stdout mode) with the given
// extra flags from the repo root and returns the parsed config.
func runConfigGen(t *testing.T, extraFlags ...string) *visorconfig.V1 {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash-based integration test not supported on Windows")
	}
	root := repoRoot(t)
	flags := strings.Join(extraFlags, " ")
	cmd := "cd " + root + " && SKYENV= go run . cli config gen -n " + flags
	out, err := script.Exec(shell + ` -c '` + cmd + `'`).String()
	if err != nil {
		t.Fatalf("config gen failed: %v\noutput: %s", err, out)
	}
	var conf visorconfig.V1
	if err := json.Unmarshal([]byte(out), &conf); err != nil {
		t.Fatalf("failed to parse config JSON: %v\noutput: %.200s", err, out)
	}
	return &conf
}

// runConfigGenWithEnv runs config gen with a custom SKYENV file.
func runConfigGenWithEnv(t *testing.T, envContent string, extraFlags ...string) *visorconfig.V1 {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash-based integration test not supported on Windows")
	}
	root := repoRoot(t)
	dir := t.TempDir()
	envPath := filepath.Join(dir, "test-skyenv.conf")
	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}
	flags := strings.Join(extraFlags, " ")
	cmd := "cd " + root + " && SKYENV=" + envPath + " go run . cli config gen -n " + flags
	out, err := script.Exec(shell + ` -c '` + cmd + `'`).String()
	if err != nil {
		t.Fatalf("config gen failed: %v\noutput: %s", err, out)
	}
	var conf visorconfig.V1
	if err := json.Unmarshal([]byte(out), &conf); err != nil {
		t.Fatalf("failed to parse config JSON: %v\noutput: %.200s", err, out)
	}
	return &conf
}

func TestConfigGenDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	conf := runConfigGen(t)

	if conf.Common == nil {
		t.Fatal("Common config is nil")
	}
	if conf.PK.Null() {
		t.Error("PK should be generated")
	}
	if conf.Dmsg == nil {
		t.Error("Dmsg config is nil")
	}
	if conf.Transport == nil {
		t.Error("Transport config is nil")
	}
	if conf.Routing == nil {
		t.Error("Routing config is nil")
	}
	if conf.Launcher == nil {
		t.Error("Launcher config is nil")
	}

	// Proxy server (skysocks) should autostart by default
	found := false
	for _, app := range conf.Launcher.Apps {
		if app.Name == "skysocks" {
			found = true
			if !app.AutoStart {
				t.Error("skysocks should autostart by default")
			}
		}
	}
	if !found {
		t.Error("skysocks app not found in launcher config")
	}

	// VPN server should autostart by default
	found = false
	for _, app := range conf.Launcher.Apps {
		if app.Name == "vpn-server" {
			found = true
			if !app.AutoStart {
				t.Error("vpn-server should autostart by default")
			}
		}
	}
	if !found {
		t.Error("vpn-server app not found in launcher config")
	}

	if conf.Hypervisor != nil {
		t.Error("should not be hypervisor by default")
	}
	if conf.IsPublic {
		t.Error("should not be public by default")
	}
}

func TestConfigGenDisableProxyServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	conf := runConfigGen(t, "--serveproxy=false")

	for _, app := range conf.Launcher.Apps {
		if app.Name == "skysocks" {
			if app.AutoStart {
				t.Error("skysocks should NOT autostart with --serveproxy=false")
			}
			return
		}
	}
	t.Error("skysocks app not found in launcher config")
}

func TestConfigGenDisableVPNServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	conf := runConfigGen(t, "--servevpn=false")

	for _, app := range conf.Launcher.Apps {
		if app.Name == "vpn-server" {
			if app.AutoStart {
				t.Error("vpn-server should NOT autostart with --servevpn=false")
			}
			return
		}
	}
	t.Error("vpn-server app not found in launcher config")
}

func TestConfigGenHypervisor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	conf := runConfigGen(t, "--ishv")

	if conf.Hypervisor == nil {
		t.Fatal("hypervisor config should be set with --ishv")
	}
	if conf.Hypervisor.HTTPAddr == "" {
		t.Error("hypervisor HTTPAddr should not be empty")
	}
}

func TestConfigGenHypervisorAddr(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	conf := runConfigGen(t, "--ishv", "--hvaddr :9000")

	if conf.Hypervisor == nil {
		t.Fatal("hypervisor config should be set")
	}
	if conf.Hypervisor.HTTPAddr != ":9000" {
		t.Errorf("hypervisor HTTPAddr = %q, want %q", conf.Hypervisor.HTTPAddr, ":9000")
	}
}

func TestConfigGenShutdownTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	conf := runConfigGen(t, "--timeout 30s")

	if conf.ShutdownTimeout != visorconfig.Duration(30_000_000_000) {
		t.Errorf("ShutdownTimeout = %v, want 30s", conf.ShutdownTimeout)
	}
}

func TestConfigGenStunServers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	conf := runConfigGen(t, "--stun stun1.example.com:3478,stun2.example.com:3478")

	if len(conf.StunServers) != 2 {
		t.Fatalf("StunServers len = %d, want 2", len(conf.StunServers))
	}
	if conf.StunServers[0] != "stun1.example.com:3478" {
		t.Errorf("StunServers[0] = %q, want %q", conf.StunServers[0], "stun1.example.com:3478")
	}
	if conf.StunServers[1] != "stun2.example.com:3478" {
		t.Errorf("StunServers[1] = %q, want %q", conf.StunServers[1], "stun2.example.com:3478")
	}
}

func TestConfigGenMuxRoutes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	conf := runConfigGen(t, "--muxroutes 3")

	if conf.Routing == nil {
		t.Fatal("Routing config is nil")
	}
	if conf.Routing.MuxRoutes != 3 {
		t.Errorf("MuxRoutes = %d, want 3", conf.Routing.MuxRoutes)
	}
}

func TestConfigGenPublicVisor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	conf := runConfigGen(t, "--public", "--maxtransports 500", "--regtimeout 5m")

	if !conf.IsPublic {
		t.Error("IsPublic should be true")
	}
	if conf.PublicVisorConfig == nil {
		t.Fatal("PublicVisorConfig should be set")
	}
	if conf.PublicVisorConfig.MaxTransports != 500 {
		t.Errorf("MaxTransports = %d, want 500", conf.PublicVisorConfig.MaxTransports)
	}
	if conf.PublicVisorConfig.RegistrationTimeout != visorconfig.Duration(300_000_000_000) {
		t.Errorf("RegistrationTimeout = %v, want 5m", conf.PublicVisorConfig.RegistrationTimeout)
	}
}

func TestConfigGenEnvFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("bash env file test not applicable on Windows")
	}

	conf := runConfigGenWithEnv(t, `ISHYPERVISOR=true
VPNSERVER=false
PROXYSERVER=false
`)

	if conf.Hypervisor == nil {
		t.Error("ISHYPERVISOR=true should enable hypervisor")
	}
	for _, app := range conf.Launcher.Apps {
		if app.Name == "vpn-server" && app.AutoStart {
			t.Error("VPNSERVER=false should disable vpn-server autostart")
		}
		if app.Name == "skysocks" && app.AutoStart {
			t.Error("PROXYSERVER=false should disable skysocks autostart")
		}
	}
}
