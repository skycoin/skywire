package registry

import (
	"fmt"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

func getHash(val any) cipher.SHA256 {
	return cipher.SumSHA256(encoder.Serialize(val))
}

func getTestUsers(n int) (users []any) {

	users = make([]any, 0, n)

	for i := range n {

		var usr = TestUser{
			Name:   fmt.Sprintf("Alice #%d", i+15),
			Age:    uint32(i), //nolint:gosec
			Hidden: []byte("seecret"),
		}

		users = append(users, usr)

	}

	return
}
