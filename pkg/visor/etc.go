// Package visor implements skywire visor.
package visor

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/logging"
)

func logBuildInfo(mLog *logging.MasterLogger) {
	log := mLog.PackageLogger("buildinfo")
	visorBuildInfo = buildinfo.Get()
	if visorBuildInfo.Version != "unknown" {
		log.WithField(" version", visorBuildInfo.Version).WithField("built on", visorBuildInfo.Date).WithField("commit", visorBuildInfo.Commit).Info()
	}
}

func genCompletion(cmd *cobra.Command) {
	switch completion {
	case "bash":
		err := cmd.Root().GenBashCompletion(os.Stdout)
		if err != nil {
			panic(err)
		}
	case "zsh":
		err := cmd.Root().GenZshCompletion(os.Stdout)
		if err != nil {
			panic(err)
		}
	case "fish":
		err := cmd.Root().GenFishCompletion(os.Stdout, true)
		if err != nil {
			panic(err)
		}
	case "powershell":
		err := cmd.Root().GenPowerShellCompletion(os.Stdout)
		if err != nil {
			panic(err)
		}
	}
	//error on unrecognized
	if (completion != "bash") && (completion != "zsh") && (completion != "fish") && (completion != "") {
		fmt.Println("Invalid completion specified:", completion)
		os.Exit(1)
	}

}
