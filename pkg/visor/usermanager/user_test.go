// Package usermanager pkg/visor/usermanager/user_test.go
package usermanager

import (
	"bytes"
	"encoding/gob"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// nolint: funlen
func Test_checkPasswordFormat(t *testing.T) {
	tests := []struct {
		name     string
		password string
		err      error
	}{
		{
			name:     "OK",
			password: strings.Repeat("Aa1!", 4),
			err:      nil,
		},
		{
			name:     "Non ASCII",
			password: strings.Repeat("Aå1!", 4),
			err:      ErrNonASCII,
		},
		{
			name:     "Too short",
			password: "1",
			err:      ErrBadPasswordLen,
		},
		{
			name:     "Too Long",
			password: strings.Repeat("1", 100),
			err:      ErrBadPasswordLen,
		},
		{
			name:     "Only digit",
			password: strings.Repeat("1", 10),
			err:      ErrSimplePassword,
		},
		{
			name:     "Only lower",
			password: strings.Repeat("a", 10),
			err:      ErrSimplePassword,
		},
		{
			name:     "Only upper",
			password: strings.Repeat("A", 10),
			err:      ErrSimplePassword,
		},
		{
			name:     "Only special",
			password: strings.Repeat("!", 10),
			err:      ErrSimplePassword,
		},
		{
			name:     "Missing digit",
			password: strings.Repeat("Aa!", 4),
			err:      ErrSimplePassword,
		},
		{
			name:     "Missing lower",
			password: strings.Repeat("A1!", 4),
			err:      ErrSimplePassword,
		},
		{
			name:     "Missing upper",
			password: strings.Repeat("a1!", 4),
			err:      ErrSimplePassword,
		},
		{
			name:     "Missing special",
			password: strings.Repeat("Aa1", 4),
			err:      ErrSimplePassword,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.err, checkPasswordFormat(tt.password))
		})
	}
}

// legacyUser is a user as saved before Argon2id: only the SHA-256.
type legacyUser struct {
	Name   string
	PwSalt []byte
	PwHash cipher.SHA256
}

// A password saved with the old SHA-256 scheme still logs in, and is
// rehashed with Argon2id when it does.
func TestPasswordUpgrade(t *testing.T) {
	salt := cipher.RandByte(passwordSaltLen)
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(legacyUser{Name: "admin", PwSalt: salt, PwHash: cipher.SumSHA256(append([]byte("Old-pass1"), salt...))}))
	u, err := DecodeUser(buf.Bytes())
	require.NoError(t, err)
	require.Nil(t, u.PwParams)

	require.False(t, u.VerifyPassword("wrong"))
	require.True(t, u.VerifyPassword("Old-pass1"))
	require.True(t, u.UpgradePassword("Old-pass1"))
	require.Equal(t, passwordParams, *u.PwParams)
	require.Len(t, u.PwKey, passwordKeyLen)
	require.Equal(t, cipher.SHA256{}, u.PwHash, "the SHA-256 is no longer kept")

	raw, err := u.Encode()
	require.NoError(t, err)
	u, err = DecodeUser(raw)
	require.NoError(t, err)
	require.True(t, u.VerifyPassword("Old-pass1"))
	require.False(t, u.VerifyPassword("Old-pass2"))
	require.False(t, u.UpgradePassword("Old-pass1"), "a current key is left as it is")
}

func TestSetPasswordUsesArgon2id(t *testing.T) {
	var u User
	require.NoError(t, u.SetPassword("New-pass1"))
	require.NotNil(t, u.PwParams)
	require.True(t, u.VerifyPassword("New-pass1"))
	require.False(t, u.VerifyPassword("new-pass1"))
}
