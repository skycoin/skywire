// Package cmdutil pkg/cmdutil/service_flags_test.go
package cmdutil

import (
	"fmt"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestServiceFlags_Init(t *testing.T) {
	t.Run("panic_on_empty_tag", func(t *testing.T) {
		defer func() {
			r := recover()
			require.NotNil(t, r)

			err, ok := r.(error)
			require.True(t, ok)
			require.EqualError(t, err, ErrTagCannotBeEmpty.Error())
		}()

		var sf ServiceFlags
		sf.Init(&cobra.Command{}, "", "config.json")
	})

	t.Run("panic_on_invalid_tag", func(t *testing.T) {
		type testCase struct {
			tag string
			err error
		}

		testCases := []testCase{
			{tag: "abcdefghijklmnopqrstuvwxyz", err: nil},
			{tag: "ABCDEFGHIJKLMNOPQRSTUVWXYZ", err: nil},
			{tag: "0123456789", err: nil},
			{tag: "aA12bB34cC56", err: nil},
			{tag: "a_a", err: nil},
			{tag: "ab_12_34_D34_d4_1f34", err: nil},
			{tag: "aGJKN-#fn3", err: ErrTagHasInvalidChars},
			{tag: "_abc123", err: ErrTagHasMisplacedUnderscores},
			{tag: "AF3g_", err: ErrTagHasMisplacedUnderscores},
			{tag: "A__231v", err: ErrTagHasMisplacedUnderscores},
			{tag: "a32f__b", err: ErrTagHasMisplacedUnderscores},
			{tag: "B3s__21fg", err: ErrTagHasMisplacedUnderscores},
		}

		for i, tc := range testCases {
			i, tc := i, tc
			t.Run(fmt.Sprintf("%d:%s", i, tc.tag), func(t *testing.T) {
				defer func() {
					if tc.err == nil {
						require.Nil(t, recover())
						return
					}

					r := recover()
					require.NotNil(t, r)

					err, ok := r.(error)
					require.True(t, ok)
					require.EqualError(t, err, tc.err.Error())
				}()

				var sf ServiceFlags
				sf.Init(&cobra.Command{}, tc.tag, "config.json")
			})
		}
	})
}
