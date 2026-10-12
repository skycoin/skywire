// Package commands cmd/skywire-visor/commands/etc.go c4-vis-cli
package commands

import (
	"github.com/skycoin/skywire/pkg/cmdutil"

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
		err := cmd.Root().GenBashCompletion(cmdutil.Stdout())
		if err != nil {
			panic(err)
		}
	case "zsh":
		err := cmd.Root().GenZshCompletion(cmdutil.Stdout())
		if err != nil {
			panic(err)
		}
	case "fish":
		err := cmd.Root().GenFishCompletion(cmdutil.Stdout(), true)
		if err != nil {
			panic(err)
		}
	case "powershell":
		err := cmd.Root().GenPowerShellCompletion(cmdutil.Stdout())
		if err != nil {
			panic(err)
		}
	}
	//error on unrecognized
	if (completion != "bash") && (completion != "zsh") && (completion != "fish") && (completion != "") {
		cmdutil.Println("Invalid completion specified:", completion)
		cmdutil.Exit(1)
	}

}
