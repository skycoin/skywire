// Package noise pkg/noise/noise_test.go
package noise

import (
	"log"
	"os"
	"testing"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	loggingLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
	if ok {
		lvl, err := logging.LevelFromString(loggingLevel)
		if err != nil {
			log.Fatal(err)
		}
		logging.SetLevel(lvl)
	} else {
		logging.Disable()
	}

	os.Exit(m.Run())
}

func TestKKAndSecp256k1(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()

	confI := Config{
		LocalPK:   pkI,
		LocalSK:   skI,
		RemotePK:  pkR,
		Initiator: true,
	}

	confR := Config{
		LocalPK:   pkR,
		LocalSK:   skR,
		RemotePK:  pkI,
		Initiator: false,
	}

	nI, err := KKAndSecp256k1(confI)
	require.NoError(t, err)

	nR, err := KKAndSecp256k1(confR)
	require.NoError(t, err)

	// -> e, es
	msg, err := nI.MakeHandshakeMessage()
	require.NoError(t, err)
	require.Error(t, nR.ProcessHandshakeMessage(append(msg, 1)))
	require.NoError(t, nR.ProcessHandshakeMessage(msg))

	// <- e, ee
	msg, err = nR.MakeHandshakeMessage()
	require.NoError(t, err)
	require.Error(t, nI.ProcessHandshakeMessage(append(msg, 1)))
	require.NoError(t, nI.ProcessHandshakeMessage(msg))

	require.True(t, nI.HandshakeFinished())
	require.True(t, nR.HandshakeFinished())

	encrypted := nI.EncryptUnsafe([]byte("foo"))
	decrypted, err := nR.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("foo"), decrypted)

	encrypted = nR.EncryptUnsafe([]byte("bar"))
	decrypted, err = nI.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("bar"), decrypted)

	encrypted = nI.EncryptUnsafe([]byte("baz"))
	decrypted, err = nR.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("baz"), decrypted)
}

func TestXKAndSecp256k1(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()

	confI := Config{
		LocalPK:   pkI,
		LocalSK:   skI,
		RemotePK:  pkR,
		Initiator: true,
	}

	confR := Config{
		LocalPK:   pkR,
		LocalSK:   skR,
		Initiator: false,
	}

	nI, err := XKAndSecp256k1(confI)
	require.NoError(t, err)

	nR, err := XKAndSecp256k1(confR)
	require.NoError(t, err)

	// -> e, es
	msg, err := nI.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nR.ProcessHandshakeMessage(msg))

	// <- e, ee
	msg, err = nR.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nI.ProcessHandshakeMessage(msg))

	// -> s, se
	msg, err = nI.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nR.ProcessHandshakeMessage(msg))

	require.True(t, nI.HandshakeFinished())
	require.True(t, nR.HandshakeFinished())

	encrypted := nI.EncryptUnsafe([]byte("foo"))
	decrypted, err := nR.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("foo"), decrypted)

	encrypted = nR.EncryptUnsafe([]byte("bar"))
	decrypted, err = nI.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("bar"), decrypted)

	encrypted = nI.EncryptUnsafe([]byte("baz"))
	decrypted, err = nR.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("baz"), decrypted)
}
