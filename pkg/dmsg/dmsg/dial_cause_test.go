// Package dmsg pkg/dmsg/dmsg/dial_cause_test.go c1-net-dmsg
package dmsg

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDialLadderErrKeepsCodeAndCause(t *testing.T) {
	t.Run("no cause returns the bare sentinel", func(t *testing.T) {
		err := dialLadderErr(nil)
		require.ErrorIs(t, err, ErrCannotConnectToDelegated)
		require.Equal(t, ErrCannotConnectToDelegated.Error(), err.Error())
	})

	t.Run("a cause is carried without breaking errors.Is", func(t *testing.T) {
		err := dialLadderErr(ErrReqNoListener)
		// The 202 code every caller matches on still matches.
		require.ErrorIs(t, err, ErrCannotConnectToDelegated)
		// And the reason is now reachable, both textually and by errors.Is.
		require.ErrorIs(t, err, ErrReqNoListener)
		require.True(t, strings.Contains(err.Error(), "no associated listener"),
			"cause should be visible in the message, got %q", err.Error())
	})

	t.Run("a non-dmsg cause is carried too", func(t *testing.T) {
		cause := errors.New("connect: connection refused")
		err := dialLadderErr(cause)
		require.ErrorIs(t, err, ErrCannotConnectToDelegated)
		require.True(t, strings.Contains(err.Error(), "connection refused"))
	})
}

func TestErrorIsMatchesOnCodeOnly(t *testing.T) {
	// Wrapping must not defeat sentinel matching: Error is a comparable
	// struct, so without Error.Is the wrapped value would compare unequal.
	require.ErrorIs(t, ErrReqNoListener.Wrap(errors.New("x")), ErrReqNoListener)
	require.NotErrorIs(t, ErrReqNoListener, ErrCannotConnectToDelegated)
}

func TestNoteDialCauseKeepsTheFirst(t *testing.T) {
	var got error
	first := errors.New("first")
	noteDialCause(&got, nil)
	require.NoError(t, got)
	noteDialCause(&got, first)
	noteDialCause(&got, errors.New("second"))
	require.Equal(t, first, got)
}
