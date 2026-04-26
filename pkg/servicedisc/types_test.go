// Package servicedisc pkg/servicedisc/types_test.go
package servicedisc

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestProxy_MarshalBinary(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	fmt.Println("PK:", pk)

	addr := NewSWAddr(pk, 23)
	fmt.Println("ADDR:", addr.String())

	ps := Service{
		Addr: addr,
	}

	data, err := ps.MarshalBinary()
	require.NoError(t, err)
	fmt.Println("RAW:", data)
}
