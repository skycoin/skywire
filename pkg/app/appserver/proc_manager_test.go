package appserver

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcManager_ProcByName(t *testing.T) {
	mI, err := NewProcManager(nil, nil, nil, ":0")
	require.NoError(t, err)

	m, ok := mI.(*procManager)
	require.True(t, ok)

	appName := "app"

	_, ok = m.ProcByName(appName)
	require.False(t, ok)

	m.mx.Lock()
	m.procs[appName] = nil
	m.mx.Unlock()

	_, ok = m.ProcByName(appName)
	require.True(t, ok)
}

func TestProcManager_Range(t *testing.T) {
	mI, err := NewProcManager(nil, nil, nil, ":0")
	require.NoError(t, err)

	m, ok := mI.(*procManager)
	require.True(t, ok)

	appNames := []string{"app1", "app2", "app3"}

	for _, n := range appNames {
		m.procs[n] = nil
	}

	var gotAppNames []string

	next := func(name string, app *Proc) bool {
		gotAppNames = append(gotAppNames, name)

		require.Nil(t, app)

		return true
	}

	m.Range(next)

	sort.Strings(gotAppNames)
	require.Equal(t, gotAppNames, appNames)
}

func TestProcManager_Pop(t *testing.T) {
	mI, err := NewProcManager(nil, nil, nil, ":0")
	require.NoError(t, err)

	m, ok := mI.(*procManager)
	require.True(t, ok)

	appName := "app"

	app, err := m.pop(appName)
	require.Equal(t, err, errNoSuchApp)
	require.Nil(t, app)

	m.procs[appName] = nil

	app, err = m.pop(appName)
	require.NoError(t, err)
	require.Nil(t, app)

	_, ok = m.procs[appName]
	require.False(t, ok)
}

func TestProcManager_SetDetailedStatus(t *testing.T) {
	mI, err := NewProcManager(nil, nil, nil, ":0")
	require.NoError(t, err)

	m, ok := mI.(*procManager)
	require.True(t, ok)

	appName := "app"

	m.procs[appName] = &Proc{}

	wantStatus := "status"
	err = m.SetDetailedStatus(appName, wantStatus)
	require.NoError(t, err)

	m.procs[appName].statusMx.RLock()
	gotStatus := m.procs[appName].status
	m.procs[appName].statusMx.RUnlock()
	require.Equal(t, wantStatus, gotStatus)

	nonExistingAppName := "none"
	err = m.SetDetailedStatus(nonExistingAppName, wantStatus)
	require.Equal(t, errNoSuchApp, err)
}

func TestProcManager_DetailedStatus(t *testing.T) {
	mI, err := NewProcManager(nil, nil, nil, ":0")
	require.NoError(t, err)

	m, ok := mI.(*procManager)
	require.True(t, ok)

	appName := "app"
	wantStatus := "status"

	m.procs[appName] = &Proc{
		status: wantStatus,
	}

	gotStatus, err := m.DetailedStatus(appName)
	require.NoError(t, err)
	require.Equal(t, wantStatus, gotStatus)

	nonExistingAppName := "none"
	_, err = m.DetailedStatus(nonExistingAppName)
	require.Equal(t, errNoSuchApp, err)
}
