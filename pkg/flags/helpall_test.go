package flags

import (
	"io"
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

func TestInstallHelpTreeReachesLeaves(t *testing.T) {
	ran := false
	leaf := &cobra.Command{Use: "visor", Run: func(*cobra.Command, []string) { ran = true }}
	raw := &cobra.Command{Use: "jq", DisableFlagParsing: true, Run: func(*cobra.Command, []string) {}}
	root := &cobra.Command{Use: "skywire"}
	root.AddCommand(leaf, raw)

	InstallHelpTree(root)
	InstallHelpTree(root)

	help := findChild(leaf, "help")
	if help == nil {
		t.Fatal("runnable leaf has no help command")
	}
	if findChild(leaf, "tree") != nil {
		t.Fatal("leaf got a tree command")
	}
	if findChild(help, "help") != nil {
		t.Fatal("repeat InstallHelpTree gave help its own help")
	}
	if findChild(raw, "help") != nil {
		t.Fatal("command with DisableFlagParsing got a help command")
	}

	root.SetArgs([]string{"visor", "help"})
	root.SetOut(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("`visor help` ran the visor")
	}
}
