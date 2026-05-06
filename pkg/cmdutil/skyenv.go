// Package cmdutil pkg/cmdutil/skyenv.go
// Shared SKYENV config file evaluation helpers.
// Used by skywire cli config gen and dmsg commands.
package cmdutil

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bitfield/script"

	"github.com/skycoin/skywire/pkg/skyenv"
)

// SkyenvValue sources a SKYENV file and evaluates a bash variable expression,
// returning the raw string result. This is the common core for all typed helpers.
//
// One level of SKYENV= redirect is honored: if the sourced envfile sets
// SKYENV to a different existing path (e.g. /etc/skywire.conf saying
// SKYENV=/home/operator/.config/skywire.conf), that file is sourced
// after the original so its values win. Recursion stops there — a
// chain `/etc/skywire.conf` → `~/.config/skywire.conf` →
// `~/.config/skywire-test.conf` would only follow the first hop.
func SkyenvValue(expr, envfile string) (string, error) {
	if skyenv.OS == "windows" {
		variable := expr
		if strings.Contains(variable, ":-") {
			parts := strings.SplitN(variable, ":-", 2)
			variable = parts[0] + "}"
		}
		out, err := script.Exec(fmt.Sprintf(
			`powershell -c "$SKYENV = '%s'; if ($SKYENV -ne '' -and (Test-Path $SKYENV)) { . $SKYENV }; if ($SKYENV -ne '%s' -and $SKYENV -ne '' -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`,
			envfile, envfile, variable,
		)).String()
		if err != nil {
			return "", err
		}
		out = strings.TrimSpace(out)
		if out == "" || out == variable {
			return "", nil
		}
		return out, nil
	}
	// First source — pulls in the original envfile's variables. If
	// the file reassigned SKYENV to a different existing path, source
	// that path next so its assignments override the originals.
	out, err := script.Exec(fmt.Sprintf(
		`bash -c 'SKYENV=%s ; ORIGINAL_SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; if [[ $SKYENV != "" ]] && [[ $SKYENV != "$ORIGINAL_SKYENV" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`,
		envfile, envfile, expr,
	)).String()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// SkyenvSlice sources a SKYENV file and expands a bash array expression
// into a string slice, one element per line. Honors the same
// one-level SKYENV= redirect as SkyenvValue.
func SkyenvSlice(expr, envfile string) ([]string, error) {
	if skyenv.OS == "windows" {
		variable := expr
		if idx := strings.Index(variable, "[@]}"); idx != -1 {
			variable = strings.TrimSuffix(variable, "[@]}")
			variable = strings.TrimSuffix(variable, "{")
		}
		return script.Exec(fmt.Sprintf(
			`powershell -c "$SKYENV = '%s'; if ($SKYENV -ne '' -and (Test-Path $SKYENV)) { . $SKYENV }; if ($SKYENV -ne '%s' -and $SKYENV -ne '' -and (Test-Path $SKYENV)) { . $SKYENV }; foreach ($item in %s) { Write-Host $item }"`,
			envfile, envfile, variable,
		)).Slice()
	}
	return script.Exec(fmt.Sprintf(
		`bash -c 'SKYENV=%s ; ORIGINAL_SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; if [[ $SKYENV != "" ]] && [[ $SKYENV != "$ORIGINAL_SKYENV" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; for _i in %s ; do echo "$_i" ; done'`,
		envfile, envfile, expr,
	)).Slice()
}

// SkyenvDefault extracts the default value from a bash ${VAR:-default} expression.
func SkyenvDefault(s string) string {
	if strings.Contains(s, ":-") {
		parts := strings.SplitN(s, ":-", 2)
		return strings.TrimRight(parts[1], "}")
	}
	return ""
}

// SkyenvString evaluates a bash variable expression and returns the string value,
// falling back to the default from the expression if unset.
func SkyenvString(s, envfile string) string {
	out, err := SkyenvValue(s, envfile)
	if err != nil || out == "" {
		return SkyenvDefault(s)
	}
	return out
}

// SkyenvBool evaluates a bash variable expression and returns a bool.
func SkyenvBool(s, envfile string) bool {
	out, err := SkyenvValue(s, envfile)
	if err != nil || out == "" {
		out = SkyenvDefault(s)
	}
	b, err := strconv.ParseBool(out)
	if err != nil {
		return false
	}
	return b
}

// SkyenvArray evaluates a bash array expression and returns a comma-separated string.
func SkyenvArray(s, envfile string) string {
	items, err := SkyenvSlice(s, envfile)
	if err != nil || len(items) == 0 {
		return ""
	}
	return strings.Join(items, ",")
}

// SkyenvInt evaluates a bash variable expression and returns an int.
func SkyenvInt(s, envfile string) int {
	out, err := SkyenvValue(s, envfile)
	if err != nil || out == "" {
		out = SkyenvDefault(s)
	}
	i, err := strconv.Atoi(out)
	if err != nil {
		return 0
	}
	return i
}

// SkyenvUint evaluates a bash variable expression and returns a uint.
func SkyenvUint(s, envfile string) uint {
	out, err := SkyenvValue(s, envfile)
	if err != nil || out == "" {
		out = SkyenvDefault(s)
	}
	i, err := strconv.ParseUint(out, 10, 64)
	if err != nil {
		return 0
	}
	return uint(i)
}

// SkyenvStringSlice evaluates a bash array expression and returns a string slice.
func SkyenvStringSlice(s, envfile string) []string {
	items, err := SkyenvSlice(s, envfile)
	if err != nil || len(items) == 0 {
		return []string{}
	}
	return items
}

// SkyenvBoolSlice evaluates a bash array expression and returns a bool slice.
func SkyenvBoolSlice(s, envfile string) []bool {
	items, err := SkyenvSlice(s, envfile)
	if err != nil {
		return []bool{false}
	}
	result := make([]bool, 0, len(items))
	for _, item := range items {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "true":
			result = append(result, true)
		default:
			result = append(result, false)
		}
	}
	if len(result) == 0 {
		return []bool{false}
	}
	return result
}

// SkyenvUintSlice evaluates a bash array expression and returns a uint slice.
func SkyenvUintSlice(s, envfile string) []uint {
	items, err := SkyenvSlice(s, envfile)
	if err != nil {
		return []uint{}
	}
	result := make([]uint, 0, len(items))
	for _, item := range items {
		num, err := strconv.ParseUint(strings.TrimSpace(item), 10, 64)
		if err == nil {
			result = append(result, uint(num))
		}
	}
	return result
}
