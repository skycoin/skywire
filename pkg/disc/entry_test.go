// Package disc pkg/disc/entry_test.go
package disc_test

import (
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/disc"
	"github.com/skycoin/skywire/pkg/logging"
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

func TestNewClientEntryIsValid(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	cases := []struct {
		name  string
		entry func() *disc.Entry
	}{
		{
			name: "NewClientEntry is valid",
			entry: func() *disc.Entry {
				return disc.NewClientEntry(pk, 0, nil)
			},
		},
		{
			name: "NewServerEntry is valid",
			entry: func() *disc.Entry {
				return disc.NewServerEntry(pk, 0, "localhost:8080", 5)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := tc.entry()
			err := entry.Sign(sk)
			require.NoError(t, err)

			err = entry.Validate(true)

			assert.NoError(t, err)
		})
	}
}

func TestVerifySignature(t *testing.T) {
	// Arrange
	// Create keys and signed entry
	pk, sk := cipher.GenerateKeyPair()
	wrongPk, _ := cipher.GenerateKeyPair()

	entry := newTestEntry(pk)

	err := entry.Sign(sk)
	require.NoError(t, err)

	// Action
	err = entry.VerifySignature()

	// Assert
	assert.Nil(t, err)

	// Action
	entry.Static = wrongPk
	err = entry.VerifySignature()

	// Assert
	assert.NotNilf(t, err, "this signature must not be valid")
}

func TestValidateRightEntry(t *testing.T) {
	// Arrange
	// Create keys and signed entry
	pk, sk := cipher.GenerateKeyPair()

	validEntry := newTestEntry(pk)
	err := validEntry.Sign(sk)
	require.NoError(t, err)

	// Action
	err = validEntry.Validate(true)
	assert.Nil(t, err)
}

func TestValidateNonKeysEntry(t *testing.T) {
	// Arrange
	// Create keys and signed entry
	_, sk := cipher.GenerateKeyPair()
	nonKeysEntry := disc.Entry{
		Timestamp: time.Now().Unix(),
		Client:    &disc.Client{},
		Server: &disc.Server{
			Address:           "localhost:8080",
			AvailableSessions: 3,
		},
		Version:  "0",
		Sequence: 0,
	}
	err := nonKeysEntry.Sign(sk)
	require.NoError(t, err)

	// Action
	err = nonKeysEntry.Validate(true)
	assert.NotNil(t, err)
}

func TestValidateNonClientNonServerEntry(t *testing.T) {
	// Arrange
	// Create keys and signed entry
	_, sk := cipher.GenerateKeyPair()
	nonClientNonServerEntry := disc.Entry{
		Timestamp: time.Now().Unix(),
		Version:   "0",
		Sequence:  0,
	}
	err := nonClientNonServerEntry.Sign(sk)
	require.NoError(t, err)

	// Action
	err = nonClientNonServerEntry.Validate(true)
	assert.NotNil(t, err)
}

func TestValidateNonSignedEntry(t *testing.T) {
	// Arrange
	// Create keys and signed entry
	nonClientNonServerEntry := disc.Entry{
		Timestamp: time.Now().Unix(),
		Version:   "0",
		Sequence:  0,
	}

	// Action
	err := nonClientNonServerEntry.Validate(true)
	assert.NotNil(t, err)
}

func TestValidateIteration(t *testing.T) {
	// Arrange
	// Create keys and two entries
	pk, sk := cipher.GenerateKeyPair()

	entryPrevious := newTestEntry(pk)
	entryNext := newTestEntry(pk)
	entryNext.Sequence = 1
	err := entryPrevious.Sign(sk)
	require.NoError(t, err)

	// Action
	err = entryPrevious.ValidateIteration(&entryNext)

	// Assert
	assert.NoError(t, err)
}

func TestValidateIterationEmptyClient(t *testing.T) {
	// Arrange
	// Create keys and two entries
	pk, sk := cipher.GenerateKeyPair()

	entryPrevious := newTestEntry(pk)
	err := entryPrevious.Sign(sk)
	require.NoError(t, err)
	entryNext := newTestEntry(pk)
	entryNext.Sequence = 1
	err = entryNext.Sign(sk)
	require.NoError(t, err)

	// Action
	errValidation := entryNext.Validate(true)
	errIteration := entryPrevious.ValidateIteration(&entryNext)

	// Assert
	assert.NoError(t, errValidation)
	assert.NoError(t, errIteration)
}

func TestValidateIterationWrongSequence(t *testing.T) {
	// Arrange
	// Create keys and two entries
	pk, sk := cipher.GenerateKeyPair()

	entryPrevious := newTestEntry(pk)
	entryPrevious.Sequence = 2
	err := entryPrevious.Sign(sk)
	require.NoError(t, err)
	entryNext := newTestEntry(pk)
	err = entryNext.Sign(sk)
	require.NoError(t, err)

	// Action
	err = entryPrevious.ValidateIteration(&entryNext)

	// Assert
	assert.NotNil(t, err)
}

func TestValidateIterationWrongTime(t *testing.T) {
	// Arrange
	// Create keys and two entries
	pk, sk := cipher.GenerateKeyPair()

	entryPrevious := newTestEntry(pk)
	err := entryPrevious.Sign(sk)
	require.NoError(t, err)
	entryNext := newTestEntry(pk)
	entryNext.Timestamp -= 3
	err = entryNext.Sign(sk)
	require.NoError(t, err)

	// Action
	err = entryPrevious.ValidateIteration(&entryNext)

	// Assert
	assert.NotNil(t, err)
}

func TestCopy(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	entry := newTestEntry(pk)
	err := entry.Sign(sk)
	require.NoError(t, err)

	cases := []struct {
		name string
		src  *disc.Entry
		dst  *disc.Entry
	}{
		{
			name: "must copy values for client, server and keys",
			src:  &entry,
			dst: &disc.Entry{
				Client:    &disc.Client{},
				Server:    &disc.Server{Address: "s", AvailableSessions: 0},
				Static:    cipher.PubKey{},
				Timestamp: 3,
				Sequence:  0,
				Version:   "0",
				Signature: "s",
			},
		},
		{
			name: "must accept dst empty entry",
			src:  &entry,
			dst:  &disc.Entry{},
		},
		{
			name: "must accept src empty entry",
			src:  &disc.Entry{},
			dst: &disc.Entry{
				Client:    &disc.Client{},
				Server:    &disc.Server{Address: "s", AvailableSessions: 0},
				Static:    cipher.PubKey{},
				Timestamp: 3,
				Sequence:  0,
				Version:   "0",
				Signature: "s",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			disc.Copy(tc.dst, tc.src)

			assert.EqualValues(t, tc.src, tc.dst)
			if tc.dst.Server != nil {
				assert.NotEqual(t, fmt.Sprintf("%p", tc.dst.Server), fmt.Sprintf("%p", tc.src.Server))
			}
			if tc.dst.Client != nil {
				assert.NotEqual(t, fmt.Sprintf("%p", tc.dst.Client), fmt.Sprintf("%p", tc.src.Client))
			}
		})
	}
}
