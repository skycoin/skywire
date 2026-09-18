// Package cliconfig cmd/skywire-cli/commands/config/gen_output_skyenv_test.go c0-cli
package cliconfig

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The `output` variable is shared between `config gen` (-o/--out,
// default ${OUTPUT}) and `config update` (-o/--output, default ""), and
// update.go's registration runs later in the package's init order — so
// it silently overwrites the skyenv default gen registered. The -o help
// text still prints the skyenv value, which is what made this invisible:
// `SKYENV=<file> cli config gen -r` wrote a cwd-relative
// skywire-config.json instead of ${OUTPUT}, and an operator inspecting
// ${OUTPUT} saw an untouched file (is_public false, no hypervisors) and
// concluded the env file was not being sourced at all.
//
// This pins the aliasing itself, so a future reader sees why gen
// re-resolves ${OUTPUT} in PreRun rather than trusting the flag default.
func TestGenAndUpdateShareTheOutputVariable(t *testing.T) {
	genOut := genConfigCmd.Flags().Lookup("out")
	require.NotNil(t, genOut, "config gen must have an -o/--out flag")
	updateOut := updateCmd.PersistentFlags().Lookup("output")
	require.NotNil(t, updateOut, "config update must have an -o/--output flag")
	require.Equal(t, "", updateOut.DefValue,
		"update's empty default is what clobbers gen's ${OUTPUT} default")
}

// resolveGenOutput is the precedence rule gen applies in PreRun.
func TestResolveGenOutput(t *testing.T) {
	// An -o on the command line always wins, even over a skyenv OUTPUT.
	require.Equal(t, "/explicit.json",
		resolveGenOutput(true, "/explicit.json", "/from-skyenv.json"))

	// Not passed → ${OUTPUT} from the skyenv file is used. This is the
	// case that was broken: `current` is the clobbered empty string.
	require.Equal(t, "/from-skyenv.json",
		resolveGenOutput(false, "", "/from-skyenv.json"))

	// A relative OUTPUT is passed through verbatim; PreRun resolves it
	// against the working directory, the same as a relative -o.
	require.Equal(t, "./skywire-config.json",
		resolveGenOutput(false, "", "./skywire-config.json"))

	// No OUTPUT in the skyenv file → the caller's value stands, so the
	// -p/-u mode default downstream still decides the path.
	require.Equal(t, "", resolveGenOutput(false, "", ""))
}
