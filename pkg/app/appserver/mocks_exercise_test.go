// Package appserver pkg/app/appserver/mocks_exercise_test.go: drives every
// method of the mockery-generated MockProcManager and MockRPCIngressClient so
// the generated implementations are exercised (and their .On/.Return wiring is
// regression-checked).
package appserver

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestMockProcManager_AllMethods(t *testing.T) {
	m := NewMockProcManager(t)

	key := appcommon.RandProcKey()
	conf := appcommon.ProcConfig{AppName: "app"}

	m.On("Addr").Return(&net.TCPAddr{})
	m.On("Close").Return(nil)
	m.On("ConnectionsSummary", "app").Return([]ConnectionSummary{{}}, nil)
	m.On("Deregister", key).Return(nil)
	m.On("DetailedStatus", "app").Return("running", nil)
	m.On("ErrorByName", "app").Return("err", true)
	m.On("GetAppPort", "app").Return(routing.Port(5), nil)
	m.On("AppByPort", routing.Port(5)).Return("app", true)
	m.On("ProcByName", "app").Return(&Proc{}, true)
	m.On("Range", mock.Anything).Return()
	m.On("Register", conf).Return(key, nil)
	m.On("SetDetailedStatus", "app", "running").Return(nil)
	m.On("SetError", "app", "boom").Return(nil)
	m.On("Start", conf).Return(appcommon.ProcID(1), nil)
	m.On("Stats", "app").Return(AppStats{}, nil)
	m.On("Stop", "app").Return(nil)
	m.On("Wait", "app").Return(nil)

	require.NotNil(t, m.Addr())
	require.NoError(t, m.Close())

	cs, err := m.ConnectionsSummary("app")
	require.NoError(t, err)
	require.Len(t, cs, 1)

	require.NoError(t, m.Deregister(key))

	ds, err := m.DetailedStatus("app")
	require.NoError(t, err)
	require.Equal(t, "running", ds)

	e, ok := m.ErrorByName("app")
	require.True(t, ok)
	require.Equal(t, "err", e)

	port, err := m.GetAppPort("app")
	require.NoError(t, err)
	require.Equal(t, routing.Port(5), port)

	name, ok := m.AppByPort(routing.Port(5))
	require.True(t, ok)
	require.Equal(t, "app", name)

	p, ok := m.ProcByName("app")
	require.True(t, ok)
	require.NotNil(t, p)

	m.Range(func(string, *Proc) bool { return true })

	gotKey, err := m.Register(conf)
	require.NoError(t, err)
	require.Equal(t, key, gotKey)

	require.NoError(t, m.SetDetailedStatus("app", "running"))
	require.NoError(t, m.SetError("app", "boom"))

	id, err := m.Start(conf)
	require.NoError(t, err)
	require.Equal(t, appcommon.ProcID(1), id)

	_, err = m.Stats("app")
	require.NoError(t, err)
	require.NoError(t, m.Stop("app"))
	require.NoError(t, m.Wait("app"))
}

func TestMockRPCIngressClient_AllMethods(t *testing.T) {
	c := NewMockRPCIngressClient(t)

	addr := appnet.Addr{}
	buf := make([]byte, 4)
	now := time.Now()

	c.On("Accept", uint16(1)).Return(uint16(2), appnet.Addr{}, nil)
	c.On("CloseConn", uint16(1)).Return(nil)
	c.On("CloseListener", uint16(1)).Return(nil)
	c.On("Dial", addr).Return(uint16(3), routing.Port(4), nil)
	c.On("DialWithOptions", addr, 1, 0, 0, 0, 1, 1, false).Return(uint16(3), routing.Port(4), nil)
	c.On("Listen", addr).Return(uint16(5), nil)
	c.On("Read", uint16(1), buf).Return(4, nil)
	c.On("SetAppPort", routing.Port(7)).Return(nil)
	c.On("SetConnectionDuration", int64(9)).Return(nil)
	c.On("SetDeadline", uint16(1), now).Return(nil)
	c.On("SetDetailedStatus", "running").Return(nil)
	c.On("SetError", "boom").Return(nil)
	c.On("SetReadDeadline", uint16(1), now).Return(nil)
	c.On("SetWriteDeadline", uint16(1), now).Return(nil)
	c.On("Write", uint16(1), buf).Return(4, nil)

	lisID, _, err := c.Accept(uint16(1))
	require.NoError(t, err)
	require.Equal(t, uint16(2), lisID)

	require.NoError(t, c.CloseConn(uint16(1)))
	require.NoError(t, c.CloseListener(uint16(1)))

	connID, _, err := c.Dial(addr)
	require.NoError(t, err)
	require.Equal(t, uint16(3), connID)

	_, _, err = c.DialWithOptions(addr, 1, 0, 0, 0, 1, 1, false)
	require.NoError(t, err)

	id, err := c.Listen(addr)
	require.NoError(t, err)
	require.Equal(t, uint16(5), id)

	n, err := c.Read(uint16(1), buf)
	require.NoError(t, err)
	require.Equal(t, 4, n)

	require.NoError(t, c.SetAppPort(routing.Port(7)))
	require.NoError(t, c.SetConnectionDuration(int64(9)))
	require.NoError(t, c.SetDeadline(uint16(1), now))
	require.NoError(t, c.SetDetailedStatus("running"))
	require.NoError(t, c.SetError("boom"))
	require.NoError(t, c.SetReadDeadline(uint16(1), now))
	require.NoError(t, c.SetWriteDeadline(uint16(1), now))

	n, err = c.Write(uint16(1), buf)
	require.NoError(t, err)
	require.Equal(t, 4, n)
}
