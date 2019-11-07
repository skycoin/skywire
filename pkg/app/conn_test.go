package app

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConn_Read(t *testing.T) {
	connID := uint16(1)

	tt := []struct {
		name     string
		readBuff []byte
		readN    int
		readErr  error
	}{
		{
			name:     "ok",
			readBuff: make([]byte, 10),
			readN:    2,
		},
		{
			name:     "read error",
			readBuff: make([]byte, 10),
			readErr:  errors.New("read error"),
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			rpc := &MockRPCClient{}
			rpc.On("Read", connID, tc.readBuff).Return(tc.readN, tc.readErr)

			conn := &Conn{
				id:  connID,
				rpc: rpc,
			}

			n, err := conn.Read(tc.readBuff)
			require.Equal(t, tc.readErr, err)
			require.Equal(t, tc.readN, n)
		})
	}
}

func TestConn_Write(t *testing.T) {
	connID := uint16(1)

	tt := []struct {
		name      string
		writeBuff []byte
		writeN    int
		writeErr  error
	}{
		{
			name:      "ok",
			writeBuff: make([]byte, 10),
			writeN:    2,
		},
		{
			name:      "write error",
			writeBuff: make([]byte, 10),
			writeErr:  errors.New("write error"),
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			rpc := &MockRPCClient{}
			rpc.On("Write", connID, tc.writeBuff).Return(tc.writeN, tc.writeErr)

			conn := &Conn{
				id:  connID,
				rpc: rpc,
			}

			n, err := conn.Write(tc.writeBuff)
			require.Equal(t, tc.writeErr, err)
			require.Equal(t, tc.writeN, n)
		})
	}
}

func TestConn_Close(t *testing.T) {
	connID := uint16(1)

	var noErr error

	t.Run("ok", func(t *testing.T) {
		rpc := &MockRPCClient{}
		rpc.On("CloseConn", connID).Return(noErr)

		conn := &Conn{
			id:       connID,
			rpc:      rpc,
			freeConn: func() bool { return true },
		}

		err := conn.Close()
		require.NoError(t, err)
	})

	t.Run("close error", func(t *testing.T) {
		closeErr := errors.New("close error")

		rpc := &MockRPCClient{}
		rpc.On("CloseConn", connID).Return(closeErr)

		conn := &Conn{
			id:       connID,
			rpc:      rpc,
			freeConn: func() bool { return true },
		}

		err := conn.Close()
		require.Equal(t, closeErr, err)
	})

	t.Run("already closed", func(t *testing.T) {
		rpc := &MockRPCClient{}
		rpc.On("CloseConn", connID).Return(noErr)

		conn := &Conn{
			id:       connID,
			rpc:      rpc,
			freeConn: func() bool { return false },
		}

		err := conn.Close()
		require.Error(t, err)
		require.Equal(t, "conn is already closed", err.Error())
	})
}
