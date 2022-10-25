// Package appnet pkg/app/appnet/type_test.go
package appnet

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestType_IsValid(t *testing.T) {
	tt := []struct {
		name string
		t    Type
		want bool
	}{
		{
			name: "valid",
			t:    TypeDmsg,
			want: true,
		},
		{
			name: "not valid",
			t:    "not valid",
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			valid := tc.t.IsValid()
			require.Equal(t, tc.want, valid)
		})
	}
}
