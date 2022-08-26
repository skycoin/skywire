package internal

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
)

var log = logging.MustGetLogger("skywire-cli")

// JSONOutput prints the cli output in json if true
var JSONOutput bool

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
func PrintOutput(output interface{}, isJSON bool) {
	if isJSON {
		outputJSON := CLIOutput{
			Output: output,
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
