// Package clisurvey cmd/skywire-cli/commands/survey/root.go
package clisurvey

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	//	pk       cipher.PubKey
	//	pkString string
	isCksum bool
)

func init() {
	surveyCmd.Flags().SortFlags = false
	surveyCmd.Flags().BoolVarP(&isCksum, "sha", "s", false, "generate checksum of system survey")

}

// RootCmd is surveyCmd
var RootCmd = surveyCmd

var surveyCmd = &cobra.Command{
	Use:                   "survey",
	DisableFlagsInUseLine: true,
	Short:                 "system survey",
	Long:                  "print the system survey",
	Run: func(cmd *cobra.Command, args []string) {
		survey, err := visorconfig.SystemSurvey()
		if err != nil {
			internal.Catch(cmd.Flags(), fmt.Errorf("Failed to generate system survey: %v", err))
		}
		//		//non-critical logic implemented with bitfield/script
		//		pkString, err = script.Exec(`skywire-cli visor pk -p`).String()
		//		//fail silently or proceed on nil error
		//		if err != nil {
		//			internal.Catch(cmd.Flags(), fmt.Errorf("failed to fetch visor public key: %v", err))
		//		} else {
		//			err = pk.Set(pkString)
		//			if err != nil {
		//			internal.Catch(cmd.Flags(), fmt.Errorf("failed to validate visor public key: %v", err))
		//		} else {
		//				survey.PubKey = pk
		//			}
		//		}
		skyaddr, err := os.ReadFile(visorconfig.PackageConfig().LocalPath + "/" + visorconfig.RewardFile) //nolint
		if err == nil {
			survey.SkycoinAddress = string(skyaddr)
		}

		s, err := json.MarshalIndent(survey, "", "\t")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Could not marshal json: %v", err))
		}
		if isCksum {
			fmt.Printf("%x/n", sha256.Sum256([]byte(s)))
		} else {
			fmt.Printf("%s", s)
		}
	},
}
