package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDmsgwebConfFile verifies that dmsgweb reads defaults from a conf file
// specified via the DMSGWEB env var and reflects them in --help output.
func TestDmsgwebConfFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based conf file not applicable on Windows")
	}

	dir := t.TempDir()
	confPath := filepath.Join(dir, "dmsgweb-test.conf")
	confContent := `WEBPORT=(9090 9091)
PROXYPORT=5555
ADDPROXY='127.0.0.1:1080'
`
	if err := os.WriteFile(confPath, []byte(confContent), 0600); err != nil {
		t.Fatal(err)
	}

	// Run dmsgweb --help with the conf file
	cmd := exec.Command("go", "run", "./cmd/dmsg-commands/dmsgweb", "--help")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "DMSGWEB="+confPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// --help exits with code 0 normally, but some cobra versions exit non-zero
		if !strings.Contains(string(out), "Usage:") {
			t.Fatalf("dmsgweb --help failed: %v\noutput: %s", err, out)
		}
	}

	helpText := string(out)

	// Check that conf file values appear as defaults in help output
	checks := []struct {
		desc   string
		substr string
	}{
		{"WEBPORT should be 9090,9091", "[9090,9091]"},
		{"PROXYPORT should be 5555", "5555"},
		{"ADDPROXY should be 127.0.0.1:1080", "127.0.0.1:1080"},
	}

	for _, c := range checks {
		if !strings.Contains(helpText, c.substr) {
			t.Errorf("%s: expected %q in help output, not found.\nHelp output:\n%s", c.desc, c.substr, helpText)
		}
	}
}

// TestDmsgwebSrvConfFile verifies that dmsgweb srv reads defaults from a conf file
// specified via the DMSGWEBSRV env var and reflects them in --help output.
func TestDmsgwebSrvConfFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based conf file not applicable on Windows")
	}

	dir := t.TempDir()
	confPath := filepath.Join(dir, "dmsgwebsrv-test.conf")
	confContent := `DMSGPORT=(8888 8889)
LOCALPORT=(7070 7071)
WHITELISTPKS=('02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5')
`
	if err := os.WriteFile(confPath, []byte(confContent), 0600); err != nil {
		t.Fatal(err)
	}

	// Run dmsgweb srv --help with the conf file
	cmd := exec.Command("go", "run", "./cmd/dmsg-commands/dmsgweb", "srv", "--help")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "DMSGWEBSRV="+confPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if !strings.Contains(string(out), "Usage:") {
			t.Fatalf("dmsgweb srv --help failed: %v\noutput: %s", err, out)
		}
	}

	helpText := string(out)

	checks := []struct {
		desc   string
		substr string
	}{
		{"DMSGPORT should be 8888,8889", "[8888,8889]"},
		{"LOCALPORT should be 7070,7071", "[7070,7071]"},
		{"WHITELISTPKS should contain the PK", "02a49bc0aa1b5b78"},
	}

	for _, c := range checks {
		if !strings.Contains(helpText, c.substr) {
			t.Errorf("%s: expected %q in help output, not found.\nHelp output:\n%s", c.desc, c.substr, helpText)
		}
	}
}

// repoRoot walks up from cwd to find go.mod
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
