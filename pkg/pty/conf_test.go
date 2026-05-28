// Package pty pkg/pty/conf_test.go
package pty_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/pty"
)

func TestParseWindowsConf(t *testing.T) {
	homedrive := "%homedrive%%homepath%\\pty.sock"
	result := pty.ParseWindowsEnv(homedrive)
	require.NotEqual(t, "", result)
}
