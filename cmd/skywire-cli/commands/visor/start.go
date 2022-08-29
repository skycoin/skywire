package clivisor

import (
	"fmt"
	"os/user"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var sourcerun bool
var root bool

func init() {
	usrLvl, err := user.Current()
	if err != nil {
		panic(err)
	}
	if usrLvl.Username == "root" {
		root = true
	}
	RootCmd.AddCommand(startCmd)
	startCmd.Flags().BoolVarP(&sourcerun, "src", "s", false, "'go run' external commands from the skywire sources")
}

// TODO(ersonp): get help from moses for it's usage
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a visor",
	Run: func(cmd *cobra.Command, args []string) {
		var output string
		var err error
		if !sourcerun {
			if root {
				//if skywire is installed as a command from a package, we can use the -p flag here
				output, err = script.Exec(`skywire-visor -p`).String()
			} else {
				//if the config exists in the userspace and this command was not run as root
				output, err = script.Exec(`skywire-visor -u`).String()
			}
		} else {
			output, err = script.Exec(`bash -c 'go run cmd/skywire-visor/skywire-visor.go'`).String()
		}
		if err != nil {
			internal.PrintError(cmd.Flags(), fmt.Errorf("Failed to start visor: %v", err))
		}
		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintln(output))
	},
}
