// Package internal contain functions used internally by different skywire-cli subcommands
package internal

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
)

var log = logging.MustGetLogger("skywire-cli")

// JSONString is the name of the json flag
var JSONString = "json"

// Catch handles errors for skywire-cli commands packages
func Catch(cmdFlags *pflag.FlagSet, err error) {
	if err != nil {
		PrintError(cmdFlags, err)
	}
}

// PrintError prints errors for skywire-cli commands packages
func PrintError(cmdFlags *pflag.FlagSet, err error) {
	isJSON, _ := cmdFlags.GetBool(JSONString) //nolint:errcheck
	if isJSON {
		errJSON := CLIOutput{
			Err: err.Error(),
		}
		b, err := json.MarshalIndent(errJSON, "", "  ")
		if err != nil {
			fmt.Println(err)
		}
		fmt.Print(string(b) + "\n")
		os.Exit(1)
	}
	log.Fatal(err)
}

// ParsePK parses a public key
func ParsePK(cmdFlags *pflag.FlagSet, name, v string) cipher.PubKey {
	var pk cipher.PubKey
	err := pk.Set(v)
	if err != nil {
		PrintError(cmdFlags, fmt.Errorf("failed to parse <%s>: %v", name, err))
	}
	return pk
}

// ParseUUID parses a uuid
func ParseUUID(cmdFlags *pflag.FlagSet, name, v string) uuid.UUID {
	id, err := uuid.Parse(v)
	if err != nil {
		PrintError(cmdFlags, fmt.Errorf("failed to parse <%s>: %v", name, err))
	}
	return id
}

// CLIOutput ss
type CLIOutput struct {
	Output interface{} `json:"output,omitempty"`
	Err    string      `json:"error,omitempty"`
}

// PrintOutput ss
func PrintOutput(cmdFlags *pflag.FlagSet, outputJSON, output interface{}) {
	isJSON, _ := cmdFlags.GetBool(JSONString) //nolint:errcheck
	if isJSON {
		outputJSON := CLIOutput{
			Output: outputJSON,
		}
		b, err := json.MarshalIndent(outputJSON, "", "  ")
		if err != nil {
			fmt.Println(err)
		}
		fmt.Print(string(b) + "\n")
		return
	}
	if output != "" {
		fmt.Print(output)
	}
}
