package proxydisc

import (
	"fmt"
	"testing"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/stretchr/testify/require"
)

func TestProxy_MarshalBinary(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	fmt.Println("PK:", pk)

	addr := NewSWAddr(pk, 23)
	fmt.Println("ADDR:", addr.String())

	ps := Proxy{
		Addr: addr,
	}

	data, err := ps.MarshalBinary()
	require.NoError(t, err)
	fmt.Println("RAW:", data)
}
