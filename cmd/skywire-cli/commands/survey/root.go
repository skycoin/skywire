// Package clisurvey cmd/skywire-cli/commands/survey/root.go
package clisurvey

import (
	"encoding/json"
	"fmt"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	pk       cipher.PubKey
	pkString string
)

func init() {
	surveyCmd.Flags().SortFlags = false
}

// RootCmd is surveyCmd
var RootCmd = surveyCmd

var surveyCmd = &cobra.Command{
	Use:                   "survey",
	DisableFlagsInUseLine: true,
	Short:                 "system survey",
	Long:                  "print the system survey",
	Run: func(cmd *cobra.Command, args []string) {
		survey, err := skyenv.SystemSurvey()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to generate system survey: %v", err))
		}

		//non-critical logic implemented with bitfield/script
		pkString, err = script.Exec(`skywire-cli visor pk -p`).String()
		//fail silently or proceed on nil error
		if err == nil {
			err = pk.Set(pkString)
			if err == nil {
				survey.PubKey = pk
			}
		}

		s, err := json.MarshalIndent(survey, "", "\t")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Could not marshal json: %v", err))
		}
		fmt.Printf("%s", s)
		//internal.PrintOutput(cmd.Flags(), s, s)
	},
}
