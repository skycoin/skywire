// Package flags pkg/flags/seckey_test.go c0-com-util
package flags

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
)

const testSK = "c1d4b8f0e2a6937d5c8b1f0a3e7d29b46c5f8a1d0e3b7c9a2f4d6e8b0c1a3f57"

// skyenvWith writes a skyenv file holding body and points SKYENV at it.
func skyenvWith(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "skywire.conf")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKYENV", p)
}

// cmdWithSK builds a command carrying an --sk flag of the real type.
func cmdWithSK() (*cobra.Command, *cipher.SecKey) {
	var sk cipher.SecKey
	c := &cobra.Command{Use: "t"}
	c.Flags().VarP(&sk, "sk", "s", "secret key")
	return c, &sk
}

func TestSecKeyFilledFromSkyenv(t *testing.T) {
	skyenvWith(t, "VISORISPUBLIC=true\nSK='"+testSK+"'\n")

	c, sk := cmdWithSK()
	if err := fillSecKeys(c); err != nil {
		t.Fatal(err)
	}
	if got := sk.Hex(); got != testSK {
		t.Errorf("sk = %s, want %s", got, testSK)
	}
}

// Without SKYENV the fallback does not fire, so a command keeps whatever it
// would have done on its own — usually generating an ephemeral key.
func TestNoSkyenvNoFill(t *testing.T) {
	t.Setenv("SKYENV", "")

	c, sk := cmdWithSK()
	if err := fillSecKeys(c); err != nil {
		t.Fatal(err)
	}
	if !isAllZeros(sk.Hex()) {
		t.Errorf("sk was filled without SKYENV set: %s", sk.Hex())
	}
}

// An explicit --sk outranks the file.
func TestExplicitFlagWins(t *testing.T) {
	skyenvWith(t, "SK='"+testSK+"'\n")
	other := "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

	c, sk := cmdWithSK()
	if err := c.ParseFlags([]string{"--sk", other}); err != nil {
		t.Fatal(err)
	}
	if err := fillSecKeys(c); err != nil {
		t.Fatal(err)
	}
	if got := sk.Hex(); got != other {
		t.Errorf("file overrode an explicit --sk: got %s", got)
	}
}

// A commented-out SK is a value the operator turned off.
func TestCommentedSKIgnored(t *testing.T) {
	skyenvWith(t, "#SK='"+testSK+"'\n")

	c, sk := cmdWithSK()
	if err := fillSecKeys(c); err != nil {
		t.Fatal(err)
	}
	if !isAllZeros(sk.Hex()) {
		t.Errorf("commented SK was used: %s", sk.Hex())
	}
}

// A trailing comment on the assignment is not part of the value.
func TestTrailingCommentStripped(t *testing.T) {
	skyenvWith(t, "SK='"+testSK+"' # the visor's own key\n")

	c, sk := cmdWithSK()
	if err := fillSecKeys(c); err != nil {
		t.Fatal(err)
	}
	if got := sk.Hex(); got != testSK {
		t.Errorf("sk = %s, want %s", got, testSK)
	}
}

// A malformed key is reported against the file it came from rather than
// silently leaving the flag zero.
func TestBadKeyReported(t *testing.T) {
	skyenvWith(t, "SK='not-a-key'\n")

	c, _ := cmdWithSK()
	err := fillSecKeys(c)
	if err == nil {
		t.Fatal("expected an error for a malformed SK")
	}
	if got := err.Error(); got == "" {
		t.Errorf("empty error message")
	}
}

// The wrapper must not drop a PersistentPreRunE the command already had.
func TestExistingPreRunPreserved(t *testing.T) {
	skyenvWith(t, "SK='"+testSK+"'\n")

	called := false
	c, sk := cmdWithSK()
	c.PersistentPreRunE = func(*cobra.Command, []string) error { called = true; return nil }
	installSecKeyFallback(c)

	if err := c.PersistentPreRunE(c, nil); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("the command's own PersistentPreRunE was not called")
	}
	if got := sk.Hex(); got != testSK {
		t.Errorf("sk = %s, want %s", got, testSK)
	}
}

// TestEndToEndThroughCobra is the one that says the feature works rather than
// that its parts do: a root wired by InitFlags exactly as every binary wires
// its own, a subcommand carrying --sk, and the key arriving by way of cobra's
// own dispatch rather than a direct call to fillSecKeys.
//
// Worth its own test because the wiring is the part that can silently not
// happen. cobra runs only the NEAREST PersistentPreRunE in the chain, so a
// subcommand defining one of its own would shadow the root's and the fallback
// would never fire for that subtree — with no error, just an unset key.
func TestEndToEndThroughCobra(t *testing.T) {
	skyenvWith(t, "SK='"+testSK+"'\n")

	var sk cipher.SecKey
	var ran bool

	root := &cobra.Command{Use: "root"}
	sub := &cobra.Command{
		Use:  "sub",
		RunE: func(*cobra.Command, []string) error { ran = true; return nil },
	}
	sub.Flags().VarP(&sk, "sk", "s", "secret key")
	root.AddCommand(sub)

	InitFlags(root, false)

	root.SetArgs([]string{"sub"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("the subcommand never ran")
	}
	if got := sk.Hex(); got != testSK {
		t.Errorf("sk = %s, want %s — the fallback did not reach a real command", got, testSK)
	}
}

// A subcommand with a persistent hook of its own must NOT hide the root's.
//
// This is the regression test for the bug that made the feature not work at
// all: `skywire dmsg` has a PersistentPreRun for --kill's signal handling, and
// cobra normally runs only the nearest hook, so the fallback never fired for
// the whole `skywire dmsg ...` subtree — silently, with the key left unset.
// installSecKeyFallback sets cobra.EnableTraverseRunHooks to fix it; this
// fails if that is ever removed.
func TestSubcommandPreRunDoesNotShadowTheFallback(t *testing.T) {
	skyenvWith(t, "SK='"+testSK+"'\n")

	var sk cipher.SecKey
	var ownRan bool
	root := &cobra.Command{Use: "root"}
	sub := &cobra.Command{
		Use:               "sub",
		PersistentPreRunE: func(*cobra.Command, []string) error { ownRan = true; return nil },
		RunE:              func(*cobra.Command, []string) error { return nil },
	}
	sub.Flags().VarP(&sk, "sk", "s", "secret key")
	root.AddCommand(sub)

	InitFlags(root, false)
	root.SetArgs([]string{"sub"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !ownRan {
		t.Error("the subcommand's own hook did not run — traversal must add to the chain, not replace it")
	}
	if got := sk.Hex(); got != testSK {
		t.Errorf("sk = %s, want %s — a subcommand hook is hiding the root's fallback", got, testSK)
	}
}
