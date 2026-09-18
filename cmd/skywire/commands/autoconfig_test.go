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
// (#4792) and the hypervisor desk address: flags in, skywire.conf
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
		"--hvdeskaddr", ":8010",
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
		"HVDESKADDR":       "':8010'",
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
	body := "#DMSGSERVER=true\n#DMSGSERVERPUBLIC='1.2.3.4:30084'\n#TRANSPORTPORT=0\n#HVDESKADDR=':8010'\n#ISHYPERVISOR=true\n"
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
		"\nHVDESKADDR=':8010'\n",
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

	if err := cmd.ParseFlags([]string{"--no-dmsg-server"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	got := map[string]string{}
	for _, e := range collectSkyenvEdits(cmd) {
		got[e.Key] = e.Value
	}
	if got["DMSGSERVER"] != "false" {
		t.Errorf("DMSGSERVER = %q; want %q", got["DMSGSERVER"], "false")
	}
}

// TestResolveConfig_OutputOverridesModePath pins the OUTPUT rule: when
// the skyenv file sets OUTPUT, that path — not the PKGENV/USRENV
// default — is the config autoconfig generates, stat-checks and
// reports. Before this, a checkout-local skywire.conf with
// OUTPUT='./skywire-config.json' had `cli config gen` (which reads
// OUTPUT as its -o default) write the repo file while autoconfig
// looked at $HOME/skywire-config.json, so a conf edit never reached
// the visor that was actually running.
func TestResolveConfig_OutputOverridesModePath(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "skywire.conf")
	out := filepath.Join(dir, "visor.json")
	body := "USRENV=true\nOUTPUT='" + out + "'\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	t.Setenv("SKYENV", conf)

	r := resolveConfig()
	if !r.outputSet {
		t.Fatalf("outputSet = false, want true when OUTPUT is set")
	}
	if r.configPath != out {
		t.Errorf("configPath = %q, want %q", r.configPath, out)
	}
	// OUTPUT says where the JSON lives, not who runs the service.
	if !r.useUserUnit {
		t.Errorf("useUserUnit = false, want true (USRENV=true)")
	}
}

// TestResolveConfig_RelativeOutputResolvesAgainstCwd documents the
// half of the OUTPUT rule that had to be picked: a relative OUTPUT
// resolves against the process working directory, NOT the directory
// holding the skyenv file. That is what `config gen -o` does with the
// same string, and autoconfig has to agree with the generator it
// delegates to.
func TestResolveConfig_RelativeOutputResolvesAgainstCwd(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "skywire.conf")
	if err := os.WriteFile(conf, []byte("USRENV=true\nOUTPUT='./skywire-config.json'\n"), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	t.Setenv("SKYENV", conf)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	want := filepath.Join(cwd, "skywire-config.json")

	r := resolveConfig()
	if r.configPath != want {
		t.Errorf("configPath = %q, want %q (cwd-relative, not conf-dir-relative)", r.configPath, want)
	}
	if r.configPath == filepath.Join(dir, "skywire-config.json") {
		t.Errorf("configPath resolved against the skyenv file's directory; the rule is cwd")
	}
}

// TestResolveConfig_MissingSkyenvIsFlagged is the regression for the
// silent-default trap: the skyenv parser reports a file that isn't
// there as "no variables set", so a typo'd or cwd-relative SKYENV
// produced a config with no SK, no hypervisors and is_public=false
// with no diagnostic at all. resolveConfig now records the miss.
func TestResolveConfig_MissingSkyenvIsFlagged(t *testing.T) {
	t.Setenv("SKYENV", filepath.Join(t.TempDir(), "does-not-exist.conf"))
	if r := resolveConfig(); !r.skyenvMissing {
		t.Errorf("skyenvMissing = false, want true for a SKYENV that does not exist")
	}
}

// TestBuildGenArgs_OutputPinsDashO asserts the write target autoconfig
// hands the generator. With OUTPUT set it must be `-o <abs path>` and
// must NOT carry -u/-p: gen only consults those when -o is unset, and
// -u additionally forces the local hypervisor on, which would override
// ISHYPERVISOR=false from the same conf file.
func TestBuildGenArgs_OutputPinsDashO(t *testing.T) {
	out := filepath.Join(t.TempDir(), "visor.json")
	r := resolvedConfig{usrEnv: true, useUserUnit: true, outputSet: true, configPath: out}

	args, printable := buildGenArgs(r, "1", nil)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-o "+out) {
		t.Errorf("args = %v, want -o %s", args, out)
	}
	for _, unwanted := range []string{"-u", "-p"} {
		for _, a := range args {
			if a == unwanted {
				t.Errorf("args = %v, must not carry %s alongside -o", args, unwanted)
			}
		}
	}
	if !strings.Contains(strings.Join(printable, " "), "-o "+out) {
		t.Errorf("printable args = %v, want the -o the operator can paste", printable)
	}
}

// TestBuildGenArgs_NoOutputKeepsModeFlag keeps the pre-existing
// contract intact: with no OUTPUT in the conf file, -u/-p is still
// what pins the write target (the dpkg-100 fix).
func TestBuildGenArgs_NoOutputKeepsModeFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    resolvedConfig
		want string
	}{
		{"usrEnv", resolvedConfig{usrEnv: true}, "-u"},
		{"pkgEnv", resolvedConfig{pkgEnv: true}, "-p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := buildGenArgs(tc.r, "1", nil)
			found := false
			for _, a := range args {
				if a == tc.want {
					found = true
				}
				if a == "-o" {
					t.Errorf("args = %v, must not pass -o without OUTPUT", args)
				}
			}
			if !found {
				t.Errorf("args = %v, want %s", args, tc.want)
			}
		})
	}
}
