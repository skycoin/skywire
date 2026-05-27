// Package commands cmd/skywire/commands/app_delegate.go — shared
// delegating-wrapper helper for `skywire app <pair> {serve,client}`
// command trees (skysocks_pair.go, vpn_pair.go, skynet_pair.go).
//
// Pattern: build a fresh cobra.Command whose RunE parses flags onto
// the supplied target's flagset and invokes the target's Run/RunE
// directly. Skipping cobra's dispatcher avoids the recursion bug
// that bites `target.SetArgs(args); target.Execute()` once both
// wrapper and target share a parent cobra tree — Execute walks up
// to root, dispatches via os.Args, resolves back through the
// wrapper, recurses, stack-overflows. (Bug originally hit the pty
// wrapper in #2864/#2866; fix landed in #2873 alongside skysocks.)
//
// --help is intercepted up front because cobra's flag-help shortcut
// only fires through the dispatcher; without the intercept,
// `app skysocks serve --help` would hit ParseFlags's "help
// requested" error path and never reach the target's help text.
package commands

import "github.com/spf13/cobra"

func newDelegateAppCmd(use, short string, target *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:                   use,
		Short:                 short,
		DisableFlagParsing:    true,
		SilenceErrors:         true,
		SilenceUsage:          true,
		DisableSuggestions:    true,
		DisableFlagsInUseLine: true,
		RunE: func(_ *cobra.Command, args []string) error {
			for _, a := range args {
				if a == "--help" || a == "-h" {
					return target.Help()
				}
			}
			if err := target.ParseFlags(args); err != nil {
				return err
			}
			remaining := target.Flags().Args()
			if target.RunE != nil {
				return target.RunE(target, remaining)
			}
			if target.Run != nil {
				target.Run(target, remaining)
			}
			return nil
		},
	}
}
