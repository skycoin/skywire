// Package clicompletion cmd/skywire-cli/commands/completion/completion.go c4-vis-cli
package clicompletion

import (
	"os"

	"github.com/spf13/cobra"
)

// RootCmd contains commands that interact with the auto-completion scripts of skywire-cli
var RootCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate a shell completion script",
	Long: `Generate a shell completion script for skywire-cli and write it to stdout.

Load it into the current shell, or install it where your shell looks for
completions:

  # bash (current shell)
  source <(skywire-cli completion bash)
  # bash (persistent, Linux)
  skywire-cli completion bash > /etc/bash_completion.d/skywire-cli

  # zsh
  skywire-cli completion zsh > "${fpath[1]}/_skywire-cli"

  # fish
  skywire-cli completion fish > ~/.config/fish/completions/skywire-cli.fish

  # powershell
  skywire-cli completion powershell | Out-String | Invoke-Expression`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	// OnlyValidArgs rejects an unknown shell name (e.g. `completion foo`)
	// instead of silently doing nothing — ExactArgs alone doesn't validate
	// the value against ValidArgs.
	Args: cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	Run: func(cmd *cobra.Command, args []string) {
		switch args[0] {
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
	},
}
