package clirewards

import "testing"

// A word the bare command doesn't know, such as a subcommand from a newer
// build, must be refused rather than start a calculation.
func TestRootRefusesUnknownArgs(t *testing.T) {
	if err := RootCmd.Args(RootCmd, []string{"send"}); err == nil {
		t.Fatal("rewards send ran the bare calculation")
	}
	if err := RootCmd.Args(RootCmd, nil); err != nil {
		t.Fatal(err)
	}
}
