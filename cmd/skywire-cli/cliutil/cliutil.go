// Package cliutil contains functions used by different skywire-cli subcommands
package cliutil

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
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

// PrintFatalError prints errors for skywire-cli commands packages.
// In --json mode the JSON error is written to stderr (so success
// output on stdout stays parseable), then the process exits 1.
//
//nolint:errcheck
func PrintFatalError(cmdFlags *pflag.FlagSet, err error) {
	isJSON, _ := cmdFlags.GetBool(JSONString)
	if isJSON {
		writeJSONError(os.Stderr, err)
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

// PrintError prints errors for skywire-cli commands packages.
// JSON mode writes to stderr; non-JSON path uses the package logger.
//
//nolint:errcheck
func PrintError(cmdFlags *pflag.FlagSet, err error) {
	isJSON, _ := cmdFlags.GetBool(JSONString)
	if isJSON {
		writeJSONError(os.Stderr, err)
		return
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

// CLIOutput is the legacy success-envelope shape kept around so external
// callers that imported the type don't break. New code should not
// construct it; PrintOutput emits raw JSON values directly.
type CLIOutput struct {
	Output interface{} `json:"output,omitempty"`
	Err    string      `json:"error,omitempty"`
}

// PrintOutput prints the value as raw JSON (no `{"output": ...}`
// envelope) in --json mode, or the human text otherwise.
//
// Migrated from the legacy CLIOutput envelope: previously the JSON
// path emitted `{"output": <value>}`, which forced every consumer
// through `jq '.output...'`. Now consumers pipe `jq '...'` directly
// against the value — matching gh, kubectl, and stripe.
//
// Errors continue to go through PrintFatalError / PrintError, which
// emit `{"error": "..."}` to stderr — so a successful pipe sees only
// values, and an interrupted pipe sees only errors.
//
//nolint:errcheck
func PrintOutput(cmdFlags *pflag.FlagSet, outputJSON, output interface{}) {
	isJSON, _ := cmdFlags.GetBool(JSONString)
	if isJSON {
		if outputJSON == nil {
			return
		}
		b, err := json.MarshalIndent(outputJSON, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		fmt.Print(string(b) + "\n")
		return
	}
	if output != "" {
		fmt.Print(output)
	}
}

// writeJSONError emits a JSON-encoded error to w, matching the shape
// `{"error": "..."}` and including a trailing newline.
//
//nolint:errcheck
func writeJSONError(w *os.File, err error) {
	errJSON := struct {
		Err string `json:"error"`
	}{Err: err.Error()}
	b, mErr := json.MarshalIndent(errJSON, "", "  ")
	if mErr != nil {
		fmt.Fprintln(w, mErr)
		return
	}
	fmt.Fprint(w, string(b)+"\n")
}

// GetData was a direct-HTTP fetch+cache helper that has been removed.
// Callers now use clirpc.FetchCachedServiceURL which routes the fetch
// through the visor RPC → DMSG direct → HTTP fallback chain, so it
// honors whatever transport the visor is actually configured to use.
