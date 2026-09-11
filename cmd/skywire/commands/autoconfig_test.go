package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
)

// TestResolveConfig_ImplicitPkgEnvFallbackUnderRoot is the regression
// for the v1.3.51 dpkg-100 failure. Sequence that reproduces it:
//
//  1. /etc/skywire.conf exists but has PKGENV and USRENV both
//     commented out (the historical out-of-the-box shape).
//  2. autoconfig runs as root (via .deb postinst).
//  3. resolveConfig's implicit "euid==0 → pkg" fallback fires and
//     sets r.pkgEnv=true with r.configPath=/opt/skywire/skywire.json.
//  4. Before this fix, generateConfig invoked `cli config gen` with
//     no -p flag; the subprocess re-read $PKGENV from the env file,
//     got "false", and wrote the config to its own default path
//     (./skywire-config.json) instead of /opt/skywire/skywire.json.
//  5. autoconfig's post-Stat check fired FATAL → postinst exit 100.
//
// This test pins down step 3 — confirming resolveConfig still
// resolves to pkgEnv under root with a silent conf — so the fix in
// generateConfig (propagating -p explicitly) has a stable contract
// to lean on.
func TestResolveConfig_ImplicitPkgEnvFallbackUnderRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to exercise the euid==0 fallback")
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "skywire.conf")
	// Both PKGENV and USRENV commented — the historical default.
	body := "# PKGENV=true\n# USRENV=true\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	t.Setenv("SKYENV", conf)

	r := resolveConfig()
	if !r.pkgEnv {
		t.Errorf("pkgEnv = false, want true under root with silent conf")
	}
	if r.usrEnv {
		t.Errorf("usrEnv = true, want false under root with silent conf")
	}
	if !strings.Contains(r.configPath, "/opt/skywire") {
		t.Errorf("configPath = %q, want /opt/skywire/... under pkgEnv", r.configPath)
	}
}

// TestResolveConfig_ExplicitUsrEnvHonored verifies the env file
// still wins when set, so we don't accidentally regress operators
// who run autoconfig as root but want USRENV behavior.
func TestResolveConfig_ExplicitUsrEnvHonored(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "skywire.conf")
	if err := os.WriteFile(conf, []byte("USRENV=true\n"), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	t.Setenv("SKYENV", conf)

	r := resolveConfig()
	if !r.usrEnv {
		t.Errorf("usrEnv = false, want true when conf has USRENV=true")
	}
	if r.pkgEnv {
		t.Errorf("pkgEnv = true, want false when conf has only USRENV=true")
	}
}

// TestCollectSkyenvEdits_InVisorDmsgServer walks the whole generator
// path for the knobs that shipped with the in-visor dmsg server
// (#4792) and the opt-in legacy hypervisor UI: flags in, skywire.conf
// lines out. The install-page form emits exactly this command line,
// so a knob that renders in the form but produces no .conf line is
// indistinguishable from the form not offering it at all.
//
// TRANSPORTPORT is asserted alongside DMSGSERVER on purpose — the
// in-visor server shares the visor's transport port, so the pair is
// what an operator actually has to get right.
func TestCollectSkyenvEdits_InVisorDmsgServer(t *testing.T) {
	restore := autoconfigVals
	t.Cleanup(func() { autoconfigVals = restore })
	autoconfigVals = autoconfigcmd.Values{}
	cmd := autoconfigcmd.New(&autoconfigVals)

	if err := cmd.ParseFlags([]string{
		"--dmsg-server",
		"--dmsg-server-public", "1.2.3.4:30084",
		"--transport-port", "30084",
		"--legacy-hv-ui",
		"--ishv",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	got := map[string]string{}
	for _, e := range collectSkyenvEdits(cmd) {
		got[e.Key] = e.Value
	}
	for key, want := range map[string]string{
		"DMSGSERVER":       "true",
		"DMSGSERVERPUBLIC": "'1.2.3.4:30084'",
		"TRANSPORTPORT":    "30084",
		"LEGACYHVUI":       "true",
		"ISHYPERVISOR":     "true",
	} {
		if got[key] != want {
			t.Errorf("%s = %q; want %q", key, got[key], want)
		}
	}

	// And the edits land in the file the way the template expects:
	// the commented template line is replaced in place, not appended.
	dir := t.TempDir()
	conf := filepath.Join(dir, "skywire.conf")
	body := "#DMSGSERVER=true\n#DMSGSERVERPUBLIC='1.2.3.4:30084'\n#TRANSPORTPORT=0\n#LEGACYHVUI=true\n#ISHYPERVISOR=true\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	if err := updateSkyenvFile(conf, collectSkyenvEdits(cmd)); err != nil {
		t.Fatalf("updateSkyenvFile: %v", err)
	}
	out, err := os.ReadFile(conf) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatalf("read conf: %v", err)
	}
	for _, want := range []string{
		"\nDMSGSERVER=true\n",
		"\nDMSGSERVERPUBLIC='1.2.3.4:30084'\n",
		"\nTRANSPORTPORT=30084\n",
		"\nLEGACYHVUI=true\n",
	} {
		if !strings.Contains("\n"+string(out), want) {
			t.Errorf("generated skywire.conf missing %q; got:\n%s", want, out)
		}
	}
}

// TestCollectSkyenvEdits_DmsgServerModesAreDistinct pins the two
// in-visor dmsg-server modes apart. DMSGSERVERCONF runs a standalone
// server config on its OWN key; DMSGSERVER runs one on the visor's
// key. Passing the config path must not imply the boolean — config
// gen gives the path precedence, so emitting both would hide which
// identity the operator actually asked for.
func TestCollectSkyenvEdits_DmsgServerModesAreDistinct(t *testing.T) {
	restore := autoconfigVals
	t.Cleanup(func() { autoconfigVals = restore })
	autoconfigVals = autoconfigcmd.Values{}
	cmd := autoconfigcmd.New(&autoconfigVals)

	if err := cmd.ParseFlags([]string{"--dmsg-server-conf", "/etc/skywire-dmsg.json"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	got := map[string]string{}
	for _, e := range collectSkyenvEdits(cmd) {
		got[e.Key] = e.Value
	}
	if got["DMSGSERVERCONF"] != "'/etc/skywire-dmsg.json'" {
		t.Errorf("DMSGSERVERCONF = %q; want %q", got["DMSGSERVERCONF"], "'/etc/skywire-dmsg.json'")
	}
	if _, ok := got["DMSGSERVER"]; ok {
		t.Errorf("--dmsg-server-conf should not write DMSGSERVER; got %q", got["DMSGSERVER"])
	}
}

// TestCollectSkyenvEdits_NoDmsgServerWritesFalse asserts the negation
// flag turns the knob off rather than on — the classic Negate bug.
func TestCollectSkyenvEdits_NoDmsgServerWritesFalse(t *testing.T) {
	restore := autoconfigVals
	t.Cleanup(func() { autoconfigVals = restore })
	autoconfigVals = autoconfigcmd.Values{}
	cmd := autoconfigcmd.New(&autoconfigVals)

	if err := cmd.ParseFlags([]string{"--no-dmsg-server", "--no-legacy-hv-ui"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	got := map[string]string{}
	for _, e := range collectSkyenvEdits(cmd) {
		got[e.Key] = e.Value
	}
	if got["DMSGSERVER"] != "false" {
		t.Errorf("DMSGSERVER = %q; want %q", got["DMSGSERVER"], "false")
	}
	if got["LEGACYHVUI"] != "false" {
		t.Errorf("LEGACYHVUI = %q; want %q", got["LEGACYHVUI"], "false")
	}
}
