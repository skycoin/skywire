package gui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadEmbeddedIcon(t *testing.T) {
	b, err := ReadSysTrayIcon()
	require.NoError(t, err)
	require.NotEqual(t, 0, len(b))
}
