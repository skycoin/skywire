// Package cmdutil pkg/cmdutil/catch_test.go
package cmdutil

import (
	"errors"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

func TestCatch(t *testing.T) {
	fn := func(ok bool) (int, error) {
		if ok {
			return rand.Int(), nil //nolint:gosec
		}
		return 0, errors.New("not okay")
	}

	t.Run("should_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("The code paniced: %v", r)
			}
		}()
		Catch(fn(true))
	})

	t.Run("should_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("The code did not panic")
			}
		}()
		Catch(fn(false))
	})

	t.Run("in_order", func(t *testing.T) {
		const rounds = 5

		expected := cipher.RandByte(rounds)
		actual := make([]byte, 0, rounds)

		addFn := func(i int) error { //nolint:unparam
			actual = append(actual, expected[i])
			return nil
		}

		Catch(
			addFn(0),
			addFn(1),
			addFn(2),
			addFn(3),
			addFn(4))

		for i, exp := range expected {
			require.Equal(t, exp, actual[i])
		}
	})
}
