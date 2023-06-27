// Package dmsgpty pkg/dmsgpty/conf_test.go
package dmsgpty_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsgpty"
)

func TestParseWindowsConf(t *testing.T) {
	homedrive := "%homedrive%%homepath%\\dmsgpty.sock"
	result := dmsgpty.ParseWindowsEnv(homedrive)
	require.NotEqual(t, "", result)
}
