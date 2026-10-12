// Package dmsgclient pkg/dmsg/dmsgclient/exec.go c1-net-dmsg
package dmsgclient

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// ExecName returns the name of the currently running executable,
// suitable for use as cobra.Command.Use.
func ExecName() string {
	return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
}

// Execute runs the given cobra command and exits on error.
func Execute(cmd *cobra.Command) {
	if err := cmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
