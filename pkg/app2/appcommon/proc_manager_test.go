package appcommon

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skycoin/src/util/logging"
)

func TestProcManager_Exists(t *testing.T) {
	m := NewProcManager(logging.MustGetLogger("proc_manager"))

	appName := "app"

	ok := m.Exists(appName)
	require.False(t, ok)

	m.procs[appName] = nil

	ok = m.Exists(appName)
	require.True(t, ok)
}

func TestProcManager_Range(t *testing.T) {
	m := NewProcManager(logging.MustGetLogger("proc_manager"))

	wantAppNames := []string{"app1", "app2", "app3"}

	for _, n := range wantAppNames {
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
	require.Equal(t, gotAppNames, wantAppNames)
}

func TestProcManager_Pop(t *testing.T) {
	m := NewProcManager(logging.MustGetLogger("proc_manager"))

	appName := "app"

	app, err := m.pop(appName)
	require.Equal(t, err, errNoSuchApp)

	m.procs[appName] = nil

	app, err = m.pop(appName)
	require.NoError(t, err)
	require.Nil(t, app)
	_, ok := m.procs[appName]
	require.False(t, ok)
}
