package snettest

// todo: this seems like an integration test, we set up a real dmsg server here
// it doesn't test sending data, but it probably should
// for now (while refactoring snet) dmsg parts are cut out
import (
	"testing"

	"github.com/skycoin/dmsg/cipher"
)

// KeyPair holds a public/private key pair.
type KeyPair struct {
	PK cipher.PubKey
	SK cipher.SecKey
}

// GenKeyPairs generates 'n' number of key pairs.
func GenKeyPairs(n int) []KeyPair {
	pairs := make([]KeyPair, n)
	for i := range pairs {
		pk, sk, err := cipher.GenerateDeterministicKeyPair([]byte{byte(i)})
		if err != nil {
			panic(err)
		}

		pairs[i] = KeyPair{PK: pk, SK: sk}
	}

	return pairs
}

// Env contains a network test environment.
type Env struct {
}

// NewEnv creates a `network.Network` test environment.
// `nPairs` is the public/private key pairs of all the `network.Network`s to be created.
func NewEnv(t *testing.T, keys []KeyPair, networks []string) *Env {
	return nil
}
