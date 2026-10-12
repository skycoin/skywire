package flags

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestInstallHelpKeepsOwnTree(t *testing.T) {
	own := &cobra.Command{Use: "tree", Run: func(*cobra.Command, []string) {}}
	group := &cobra.Command{Use: "ping"}
	group.AddCommand(own, &cobra.Command{Use: "test", Run: func(*cobra.Command, []string) {}})

	InstallHelpTree(group)
	if got := findChild(group, "tree"); got != own {
		t.Fatalf("own tree command replaced by %q", got.Short)
	}

	plain := &cobra.Command{Use: "visor"}
	plain.AddCommand(&cobra.Command{Use: "info", Run: func(*cobra.Command, []string) {}})
	InstallHelp(plain)
	first := findChild(plain, "tree")
	InstallHelp(plain)
	if second := findChild(plain, "tree"); second == nil || second == first {
		t.Fatal("repeat InstallHelp did not replace its own tree command")
	}
}
