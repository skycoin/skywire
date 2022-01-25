//go:build systray
// +build systray

package gui

import (
	"testing"

	"github.com/skycoin/dmsg/cipher"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestReadEmbeddedIcon(t *testing.T) {
	b, err := ReadSysTrayIcon()
	require.NoError(t, err)
	require.NotEqual(t, 0, len(b))
}
