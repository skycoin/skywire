//go:build exclude

// Package cliconfig cmd/skywire-cli/commands/config/auto.go
package cliconfig

import (
	"fmt"
	"os"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
)

var (
	cmdz string
)

func init() {
	RootCmd.AddCommand(testCmd)
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "test env detection",
	Run: func(cmd *cobra.Command, args []string) {
		if _, err := os.Stat("test.sh"); err != nil {
			fmt.Printf("error: %v", err)
		} else {
			cmdz = `bash -c "source test.sh"`
			_, _ = script.Exec(cmdz).Stdout() //nolint:errcheck
		}
		fmt.Printf("os.Getenv(\"TEST\") == %s\n", os.Getenv("TEST"))
		_, _ = script.Exec("echo").Stdout()
		cmdz = `bash -c "echo ${TEST}"`
		res, err := script.Exec(cmdz).String()
		if err != nil {
			fmt.Printf("error: %v", err)
		}
		fmt.Printf("bash -c \"echo ${TEST}\" == %s\n", res)
	},
}
