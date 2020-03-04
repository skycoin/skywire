package snet

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
)

func TestDisassembleAddr(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	port := uint16(2)
	addr := dmsg.Addr{
		PK: pk, Port: port,
	}

	gotPK, gotPort, err := disassembleAddr(addr)
	require.NoError(t, err)
	require.Equal(t, pk, gotPK)
	require.Equal(t, port, gotPort)
}
