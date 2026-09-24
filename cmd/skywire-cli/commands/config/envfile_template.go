// Package cliconfig cmd/skywire-cli/commands/config/envfile_template.go c4-vis-cli
//
// The SKYENV template as a value other commands can write, rather than only
// something `config gen -q/-Q` prints.
//
// On a packaged install the template reaches /etc/skywire.conf through the
// install hook — the deb/arch packages run
// `[[ ! -f /etc/skywire.conf ]] && skywire cli config gen -pqQ /etc/skywire.conf`
// once and never touch it again, so operator edits survive every upgrade.
// Installs with no such hook (a `go install`, a release binary unpacked by
// hand, the browser visor) never get the file, and then every knob in it is
// invisible: the operator has nothing to edit and `skywire autoconfig --<flag>`
// fails because skyenvfile.Update can only edit a file that exists.
//
// Exporting the template lets `skywire autoconfig` create it on the same
// generate-once-if-absent terms, so those installs behave like a packaged one.
package cliconfig

import (
	"strings"

	"github.com/skycoin/skywire/pkg/skyenv"
)

// EnvFileTemplate returns the annotated SKYENV template for this OS with the
// install-mode line uncommented — PKGENV for package paths, USRENV for
// userspace — which is the one choice the file cannot be silent about: it
// decides where every other path resolves.
//
// Everything else is left commented at its documented default, exactly as
// `config gen -Q` writes it, so the file reads as a menu of what can be set.
func EnvFileTemplate(pkgEnv bool) string {
	tpl := envfileLinux
	key := "PKGENV"
	if !pkgEnv {
		key = "USRENV"
	}
	if skyenv.OS == "windows" {
		// The PowerShell form of the same line: `#$PKGENV=$true`.
		return uncommentLine(envfileWindows, "#$"+key+"=")
	}
	return uncommentLine(tpl, "#"+key+"=")
}

// uncommentLine strips the leading '#' from the first line whose trimmed form
// starts with prefix. Returns the template unchanged when no line matches, so
// a template edit that renames a key degrades to "nothing uncommented" rather
// than to corrupted output.
func uncommentLine(tpl, prefix string) string {
	lines := strings.Split(tpl, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			lines[i] = strings.Replace(line, "#", "", 1)
			return strings.Join(lines, "\n")
		}
	}
	return tpl
}
