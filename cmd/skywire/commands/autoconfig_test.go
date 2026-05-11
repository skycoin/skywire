package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
