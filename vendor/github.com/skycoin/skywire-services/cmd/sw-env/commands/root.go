// Package commands cmd/sw-env/commands/root.go
package commands

import (
	"fmt"
	"log"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/spf13/cobra"

	cfg "github.com/skycoin/skywire-services/internal/config"
)

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use:   "swe",
	Short: "skywire environment generator",
	Long: `
	┌─┐┬ ┬   ┌─┐┌┐┌┬  ┬
	└─┐│││───├┤ │││└┐┌┘
	└─┘└┴┘   └─┘┘└┘ └┘ `,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		switch {
		case publicFlag:
			fmt.Println(cfg.PrintJSON(cfg.DefaultPublicEnv()))
		case localFlag:
			fmt.Println(cfg.PrintJSON(cfg.DefaultLocalEnv()))
		case dockerFlag:
			fmt.Println(cfg.PrintJSON(cfg.DefaultDockerizedEnv(dockerNetwork)))
		}
	},
}

var (
	publicFlag    bool
	localFlag     bool
	dockerFlag    bool
	dockerNetwork string
)

func init() {
	RootCmd.AddCommand(
		visorCmd,
		dmsgCmd,
		setupCmd,
	)
	RootCmd.Flags().BoolVarP(&publicFlag, "public", "p", false, "Environment with public skywire-services\033[0m")
	RootCmd.Flags().BoolVarP(&localFlag, "local", "l", false, "Environment with skywire-services on localhost\033[0m")
	RootCmd.Flags().BoolVarP(&dockerFlag, "docker", "d", false, "Environment with dockerized skywire-services\033[0m")
	RootCmd.Flags().StringVarP(&dockerNetwork, "network", "n", "SKYNET", "Docker network to use\033[0m")
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+RootCmd.Use)
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

var visorCmd = &cobra.Command{
	Use:   "visor",
	Short: "Generate config for skywire-visor",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Println(cfg.PrintJSON(cfg.DefaultPublicVisorConfig()))
	},
}

var dmsgCmd = &cobra.Command{
	Use:   "dmsg",
	Short: "Generate config for dmsg-server",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Println(cfg.PrintJSON(cfg.EmptyDmsgServerConfig()))
	},
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Generate config for setup node",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Println(cfg.PrintJSON(cfg.EmptySetupNodeConfig()))
	},
}

// Execute executes root CLI command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:       RootCmd,
		Headings:      cc.HiBlue + cc.Bold, //+ cc.Underline,
		Commands:      cc.HiBlue + cc.Bold,
		CmdShortDescr: cc.HiBlue,
		Example:       cc.HiBlue + cc.Italic,
		ExecName:      cc.HiBlue + cc.Bold,
		Flags:         cc.HiBlue + cc.Bold,
		//FlagsDataType: cc.HiBlue,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

const help = "Usage:\r\n" +
	"  {{.UseLine}}{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}\r\n\r\n" +
	"Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand)}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"
