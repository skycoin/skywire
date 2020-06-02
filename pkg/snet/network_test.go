package snet

import (
	"testing"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/stretchr/testify/require"
)

func TestDisassembleAddr(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	port := uint16(2)
	addr := dmsg.Addr{
		PK: pk, Port: port,
	}

	gotPK, gotPort := disassembleAddr(addr)
	require.Equal(t, pk, gotPK)
	require.Equal(t, port, gotPort)
}
