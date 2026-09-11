// Package flags pkg/flags/seckey.go c0-com-util
//
// Secret keys given on the command line are visible to every other process on
// the machine — `ps` shows a full argv — and they land in shell history. The
// repository already has the file to keep one in: the skyenv file that
// `skywire autoconfig` writes, whose SK entry is the visor's own key. This
// fills an unset secret-key flag from it.
//
// Opt-in, by design. The fallback applies only when SKYENV names a file, not
// whenever /etc/skywire.conf happens to exist, because adopting the visor's
// key automatically would be the wrong default in a way that is hard to see:
// two dmsg clients sharing a public key race for one dmsg-discovery entry.
// dmsgpty-host's --sk-from-visor refuses that combination outright for the
// same reason. Setting SKYENV is the operator saying "this file is my
// identity", which is exactly when it is what they want.
package flags

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/skywireconfig/skyenvfile"
)

// secKeyType is the pflag type name cipher.SecKey reports. Flags are found by
// type rather than by name: --sk is the usual spelling, but the guarantee
// wanted here is "a secret key never has to be typed on a command line",
// which is about what the flag holds and not what it is called.
const secKeyType = "cipher.SecKey"

// skyenvSKKey is the entry read from the skyenv file. It is the same key
// `skywire autoconfig --sk` writes — see autoconfigcmd.EnvMap.
const skyenvSKKey = "SK"

// fillSecKeys sets any unset secret-key flag on cmd from the skyenv file.
//
// A flag the operator passed is left alone: an explicit --sk outranks the
// file, the same order dmsgpty-host uses. A flag already holding a non-zero
// value is left alone too, which covers a command that generated an ephemeral
// key into its flag variable before parsing.
func fillSecKeys(cmd *cobra.Command) error {
	if os.Getenv(skyenvfile.SKYENVVar) == "" {
		return nil
	}
	path := skyenvfile.Path()
	sk, ok := skyenvfile.Lookup(path, skyenvSKKey)
	if !ok {
		return nil
	}

	var firstErr error
	fill := func(f *pflag.Flag) {
		if firstErr != nil || f.Changed || f.Value.Type() != secKeyType {
			return
		}
		if !isAllZeros(f.Value.String()) {
			return
		}
		if err := f.Value.Set(sk); err != nil {
			firstErr = fmt.Errorf("%s: bad %s in %s: %w", f.Name, skyenvSKKey, path, err)
		}
	}
	cmd.Flags().VisitAll(fill)
	cmd.PersistentFlags().VisitAll(fill)
	return firstErr
}

// installSecKeyFallback wraps cmd's PersistentPreRunE so fillSecKeys runs
// after flag parsing and before the command does anything.
//
// Wrapping rather than assigning: a root that already has a
// PersistentPreRunE — skywire-visor does — would otherwise lose it. cobra runs
// only the nearest PersistentPreRunE in the chain, so installing this on the
// root covers every subcommand that does not define one of its own, and a
// subcommand that does keeps its behavior.
func installSecKeyFallback(cmd *cobra.Command) {
	// Run every persistent hook in the chain, not only the nearest one.
	//
	// This is load-bearing rather than tidy. cobra normally runs the FIRST
	// PersistentPreRun it finds walking up from the executed command, so a
	// subcommand that has one of its own hides the root's — and `skywire dmsg`
	// has one, for the signal handling behind --kill. That shadowed this
	// fallback across the whole `skywire dmsg ...` subtree, which is where most
	// of the secret-key flags in this repository live, and it failed silently:
	// no error, just a key that stayed unset. Verified against the built
	// binary, not reasoned about — a malformed SK in the file was accepted
	// without complaint before this line and reports the file after it.
	//
	// Nothing is lost by traversing. The only persistent hooks in this
	// repository are `skywire dmsg`'s, `cli mdisc`'s, and this one, so the
	// change is that this one now also runs; theirs still do.
	cobra.EnableTraverseRunHooks = true

	prev := cmd.PersistentPreRunE
	prevPlain := cmd.PersistentPreRun
	cmd.PersistentPreRunE = func(c *cobra.Command, args []string) error {
		if err := fillSecKeys(c); err != nil {
			return err
		}
		switch {
		case prev != nil:
			return prev(c, args)
		case prevPlain != nil:
			prevPlain(c, args)
		}
		return nil
	}
	// cobra prefers PersistentPreRunE when both are set; clearing the plain
	// one keeps it from being called twice by a future cobra that changes that
	// preference, and the wrapper above already calls it.
	cmd.PersistentPreRun = nil
}
