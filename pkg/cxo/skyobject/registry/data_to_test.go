package registry

import (
	"fmt"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

func getHash(val interface{}) cipher.SHA256 {
	return cipher.SumSHA256(encoder.Serialize(val))
}

func getTestUsers(n int) (users []interface{}) {

	users = make([]interface{}, 0, n)

	for i := 0; i < n; i++ {

		var usr = TestUser{
			Name:   fmt.Sprintf("Alice #%d", i+15),
			Age:    uint32(i),
			Hidden: []byte("seecret"),
		}

		users = append(users, usr)

	}

	return
}
