// Package internal contain functions used internally by different skywire-cli subcommands
package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/bitfield/script"
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
		PrintFatalError(cmdFlags, err)
	}
}

// PrintFatalError prints errors for skywire-cli commands packages
func PrintFatalError(cmdFlags *pflag.FlagSet, err error) {
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

// PrintFatalRPCError prints fatal RPC errors for skywire-cli commands packages
func PrintFatalRPCError(cmdFlags *pflag.FlagSet, err error) {
	PrintFatalError(cmdFlags, fmt.Errorf("Failed to connect to visor RPC or RPC method not found; is skywire running?: %v", err))
}

// PrintRPCError prints nonfatal RPC errors for skywire-cli commands packages
func PrintRPCError(cmdFlags *pflag.FlagSet, err error) {
	PrintError(cmdFlags, fmt.Errorf("Failed to connect to visor RPC or RPC method not found; is skywire running?: %v", err))
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
	}
	log.Error(err)
}

// ParsePK parses a public key
func ParsePK(cmdFlags *pflag.FlagSet, name, v string) cipher.PubKey {
	var pk cipher.PubKey
	err := pk.Set(v)
	if err != nil {
		PrintFatalError(cmdFlags, fmt.Errorf("failed to parse <%s>: %v", name, err))
	}
	return pk
}

// ParseUUID parses a uuid
func ParseUUID(cmdFlags *pflag.FlagSet, name, v string) uuid.UUID {
	id, err := uuid.Parse(v)
	if err != nil {
		PrintFatalError(cmdFlags, fmt.Errorf("failed to parse <%s>: %v", name, err))
	}
	return id
}

// CLIOutput is used to print the cli output in json
type CLIOutput struct {
	Output interface{} `json:"output,omitempty"`
	Err    string      `json:"error,omitempty"`
}

// PrintOutput prints either the normal output or the json output as per the global `--json` flag
func PrintOutput(cmdFlags *pflag.FlagSet, outputJSON, output interface{}) {
	isJSON, _ := cmdFlags.GetBool(JSONString) //nolint:errcheck
	if isJSON {
		if outputJSON != nil {
			outputJSON := CLIOutput{
				Output: outputJSON,
			}
			b, err := json.MarshalIndent(outputJSON, "", "  ")
			if err != nil {
				fmt.Println(err)
			}
			fmt.Print(string(b) + "\n")
		}
		return
	}
	if output != "" {
		fmt.Print(output)
	}
}

// GetData fetches data from the specified URL via http or from cached file
func GetData(cachefile, thisurl string, cacheFilesAge int) (thisdata string) {
	var shouldfetch bool
	buf1 := new(bytes.Buffer)
	cTime := time.Now()
	if cachefile == "" {
		thisdata, _ = script.NewPipe().WithHTTPClient(&http.Client{Timeout: 30 * time.Second}).Get(thisurl).String() //nolint
		return thisdata
	}
	if cachefile != "" {
		if u, err := os.Stat(cachefile); err != nil {
			shouldfetch = true
		} else {
			if cTime.Sub(u.ModTime()).Minutes() > float64(cacheFilesAge) {
				shouldfetch = true
			}
		}
		if shouldfetch {
			_, _ = script.NewPipe().WithHTTPClient(&http.Client{Timeout: 30 * time.Second}).Get(thisurl).Tee(buf1).WriteFile(cachefile) //nolint
			thisdata = buf1.String()
		} else {
			thisdata, _ = script.File(cachefile).String() //nolint
		}
	} else {
		thisdata, _ = script.NewPipe().WithHTTPClient(&http.Client{Timeout: 30 * time.Second}).Get(thisurl).String() //nolint
	}
	return thisdata
}
