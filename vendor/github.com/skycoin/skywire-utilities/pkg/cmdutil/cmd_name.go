package cmdutil

import (
	"os"
	"path"
)

// RootCmdName returns the root command name.
func RootCmdName() string {
	return path.Base(os.Args[0])
}
