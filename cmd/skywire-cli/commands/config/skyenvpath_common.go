// Package cliconfig cmd/skywire-cli/commands/config/skyenvpath_common.go c4-vis-cli
package cliconfig

import (
	"os"
	"path/filepath"
)

// skyenvSource is the absolute path of the SKYENV file this run read, or ""
// when it read none. The visor writes live setting changes back to it.
func skyenvSource() string {
	if skyenvfile == "" {
		return ""
	}
	p, err := filepath.Abs(skyenvfile)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}
