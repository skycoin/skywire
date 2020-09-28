package usermanager

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
