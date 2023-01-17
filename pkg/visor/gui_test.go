//go:build !withoutsystray
// +build !withoutsystray

// Package visor pkg/visor/gui_test.go
package visor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadEmbeddedIcon(t *testing.T) {
	b, err := readSysTrayIcon()
	require.NoError(t, err)
	require.NotEqual(t, 0, len(b))
}
