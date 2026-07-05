// Package appcommon pkg/app/appcommon/mocks_exercise_test.go: drives every
// method of the mockery-generated MockAddr/MockConn/MockListener so the
// generated implementations are exercised.
package appcommon

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMockAddr_AllMethods(t *testing.T) {
	m := &MockAddr{}
	m.On("Network").Return("tcp")
	m.On("String").Return("1.2.3.4:80")

	require.Equal(t, "tcp", m.Network())
	require.Equal(t, "1.2.3.4:80", m.String())
	m.AssertExpectations(t)
}

func TestMockListener_AllMethods(t *testing.T) {
	m := &MockListener{}
	addr := &net.TCPAddr{}
	conn := &net.TCPConn{}

	m.On("Accept").Return(conn, nil)
	m.On("Addr").Return(addr)
	m.On("Close").Return(nil)

	gotConn, err := m.Accept()
	require.NoError(t, err)
	require.Equal(t, conn, gotConn)
	require.Equal(t, addr, m.Addr())
	require.NoError(t, m.Close())
	m.AssertExpectations(t)
}

func TestMockConn_AllMethods(t *testing.T) {
	m := &MockConn{}
	addr := &net.TCPAddr{}
	buf := make([]byte, 4)
	now := time.Now()

	m.On("Close").Return(nil)
	m.On("LocalAddr").Return(addr)
	m.On("RemoteAddr").Return(addr)
	m.On("Read", buf).Return(4, nil)
	m.On("Write", buf).Return(4, nil)
	m.On("SetDeadline", now).Return(nil)
	m.On("SetReadDeadline", now).Return(nil)
	m.On("SetWriteDeadline", now).Return(nil)

	require.NoError(t, m.Close())
	require.Equal(t, addr, m.LocalAddr())
	require.Equal(t, addr, m.RemoteAddr())

	n, err := m.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 4, n)

	n, err = m.Write(buf)
	require.NoError(t, err)
	require.Equal(t, 4, n)

	require.NoError(t, m.SetDeadline(now))
	require.NoError(t, m.SetReadDeadline(now))
	require.NoError(t, m.SetWriteDeadline(now))
	m.AssertExpectations(t)
}
