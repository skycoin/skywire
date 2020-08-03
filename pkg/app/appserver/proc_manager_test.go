package appserver

import (
	"sort"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appcommon"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/stretchr/testify/require"
)

func TestProcManager_Exists(t *testing.T) {
	srv := New(nil, appcommon.DefaultServerAddr)

	mIfc := NewProcManager(logging.MustGetLogger("proc_manager"), srv)
	m, ok := mIfc.(*procManager)
	require.True(t, ok)

	appName := "app"

	ok = m.Exists(appName)
	require.False(t, ok)

	m.procs[appName] = nil

	ok = m.Exists(appName)
	require.True(t, ok)
}

func TestProcManager_Range(t *testing.T) {
	srv := New(nil, appcommon.DefaultServerAddr)

	mIfc := NewProcManager(logging.MustGetLogger("proc_manager"), srv)
	m, ok := mIfc.(*procManager)

	require.True(t, ok)

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
	srv := New(nil, appcommon.DefaultServerAddr)

	mIfc := NewProcManager(logging.MustGetLogger("proc_manager"), srv)
	m, ok := mIfc.(*procManager)
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
