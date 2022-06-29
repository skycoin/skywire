package clivisor

import (
	"os/user"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
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


var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a visor",
	Run: func(_ *cobra.Command, args []string) {
		var err error
		if !sourcerun {
			if root {
				//if skywire is installed as a command from a package, we can use the -p flag here
				_, err = script.Exec(`skywire-visor -p`).Stdout()
			} else {
				//if the config exists in the userspace and this command was not run as root
				_, err = script.Exec(`skywire-visor -u`).Stdout()
			}
		} else {
			_, err = script.Exec(`bash -c 'go run cmd/skywire-visor/skywire-visor.go'`).Stdout()
		}
		if err != nil {
			logger.WithError(err).Fatalln("Failed to start visor")
		}
	},
}
