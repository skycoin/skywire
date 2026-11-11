package ui2

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestBuildFlagArgs(t *testing.T) {
	flags := []visorapi.AppFlag{
		{Name: "srv", Type: "string"},
		{Name: "killswitch", Type: "bool", Default: "false"},
		{Name: "dns", Shorthand: "d", Type: "string"},
		{Name: "alias", Type: "stringList"},
	}
	args := []string{"app", "vpn-client", "--srv", "02ab", "-d=1.1.1.1", "--killswitch", "--unknown", "x"}
	got := BuildFlagArgs(args, flags,
		map[string]string{"srv": "03cd", "killswitch": "false", "alias": "a, b", "dns": ""},
		map[string]bool{"srv": true, "killswitch": true, "alias": true})
	require.Equal(t, []string{"app", "vpn-client", "--unknown", "x", "--srv=03cd", "--killswitch=false", "--alias=a", "--alias=b"}, got,
		"positional and unknown args stay, known flags are rewritten and an unset one is dropped")

	v := FlagsViewOf("pk", visorapi.AppFlags{App: "a", Flags: []visorapi.AppFlag{{Name: "l", Type: "stringList", Default: "[]"}, {Name: "s", Type: "string", Default: "x", Value: "y", Set: true}}})
	require.Equal(t, "", v.Flags[0].Value)
	require.Equal(t, "y", v.Flags[1].Value)
}
