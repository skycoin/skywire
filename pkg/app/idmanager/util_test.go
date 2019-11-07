package idmanager

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
)

func TestAssertListener(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		var ifc interface{} = &appcommon.MockListener{}

		l, err := AssertListener(ifc)
		require.NoError(t, err)
		require.Equal(t, ifc, l)
	})

	t.Run("wrong type", func(t *testing.T) {
		var ifc interface{} = "val"

		l, err := AssertListener(ifc)
		require.Error(t, err)
		require.True(t, "wrong type of value stored for listener" == err.Error())
		require.Nil(t, l)
	})
}

func TestAssertConn(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		var ifc interface{} = &appcommon.MockConn{}

		conn, err := AssertConn(ifc)
		require.NoError(t, err)
		require.Equal(t, ifc, conn)
	})

	t.Run("wrong type", func(t *testing.T) {
		var ifc interface{} = "val"

		conn, err := AssertConn(ifc)
		require.Error(t, err)
		require.True(t, "wrong type of value stored for conn" == err.Error())
		require.Nil(t, conn)
	})
}
