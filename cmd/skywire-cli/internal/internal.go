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
func Catch(err error, msgs ...string) {
	if err != nil {
		if len(msgs) > 0 {
			log.Fatalln(append(msgs, err.Error()))
		} else {
			log.Fatalln(err)
		}
	}
}

// ParsePK parses a public key
func ParsePK(name, v string) cipher.PubKey {
	var pk cipher.PubKey
	Catch(pk.Set(v), fmt.Sprintf("failed to parse <%s>:", name))
	return pk
}

// ParseUUID parses a uuid
func ParseUUID(name, v string) uuid.UUID {
	id, err := uuid.Parse(v)
	Catch(err, fmt.Sprintf("failed to parse <%s>:", name))
	return id
}

// CLIOutput ss
type CLIOutput struct {
	Output interface{} `json:"output,omitempty"`
	Err    string      `json:"error,omitempty"`
}

// PrintOutput ss
func PrintOutput(outputJSON, output interface{}, cmdFlags *pflag.FlagSet) {
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
	fmt.Println(output)
}

// PrintFatalError ss
func PrintFatalError(err error, logger *logging.Logger, cmdFlags *pflag.FlagSet) {
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
		return
	}
	logger.Fatal(err)
}
