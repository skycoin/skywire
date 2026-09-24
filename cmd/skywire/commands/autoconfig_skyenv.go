// Package commands cmd/skywire/commands/autoconfig_skyenv.go c4-vis-cli
//
// Thin aliases over pkg/skywireconfig/skyenvfile, which owns editing the
// bash-style skyenv file (/etc/skywire.conf). The visor shares that
// package so a paired hypervisor survives the next autoconfig run.
package commands

import (
	"os"
	"path/filepath"

	cliconfig "github.com/skycoin/skywire/cmd/skywire-cli/commands/config"
	"github.com/skycoin/skywire/pkg/skywireconfig/skyenvfile"
)

type skyenvEdit = skyenvfile.Edit

// ensureSkyenvFile writes the annotated SKYENV template to path when nothing
// is there yet, and reports whether it created it. An existing file — however
// it got there, and whatever the operator has since done to it — is left
// EXACTLY as it is: this is generate-once-if-absent, never an overwrite, which
// is what makes it safe to call on every autoconfig run.
//
// pkgEnv picks which install-mode line the template ships uncommented; see
// cliconfig.EnvFileTemplate.
func ensureSkyenvFile(path string, pkgEnv bool) (created bool, err error) {
	if path == "" {
		return false, nil
	}
	if _, serr := os.Stat(path); serr == nil {
		return false, nil
	} else if !os.IsNotExist(serr) {
		return false, serr
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec // /etc is world-readable by design
			return false, err
		}
	}
	// 0644: the file is a system-wide settings file meant to be readable by
	// the operator and by an unprivileged `config gen` reading its defaults,
	// matching what `config gen -Q` writes.
	if err := os.WriteFile(path, []byte(cliconfig.EnvFileTemplate(pkgEnv)+"\n"), 0644); err != nil { //nolint:gosec
		return false, err
	}
	return true, nil
}

func updateSkyenvFile(path string, edits []skyenvEdit) error { return skyenvfile.Update(path, edits) }

func formatSkyenvBool(b bool) string { return skyenvfile.FormatBool(b) }

func formatSkyenvString(s string) string { return skyenvfile.FormatString(s) }

func formatSkyenvBashArray(csv string) string { return skyenvfile.FormatBashArray(csv) }

func formatSkyenvInt(i int) string { return skyenvfile.FormatInt(i) }

func defaultSkyenvPath() string { return skyenvfile.DefaultPath() }
