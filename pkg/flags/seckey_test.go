// Package flags pkg/flags/seckey_test.go c0-com-util
package flags

import (
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
