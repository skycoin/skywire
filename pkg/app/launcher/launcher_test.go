// Package launcher pkg/app/launcher/launcher_test.go
package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
)

func testLogger() logrus.FieldLogger {
	l := logrus.New()
	l.SetOutput(os.Stderr)
	l.SetLevel(logrus.PanicLevel) // keep test output quiet
	return l
}

// newTestLauncher builds an AppLauncher wired to a MockProcManager and a
// fresh temp dir for BinPath/LocalPath, without going through NewLauncher
// (which mutates global networker state). apps is keyed by name.
func newTestLauncher(t *testing.T, procM appserver.ProcManager, apps ...appserver.AppConfig) *AppLauncher {
	t.Helper()

	dir := t.TempDir()
	appMap := make(map[string]appserver.AppConfig, len(apps))
	for _, ac := range apps {
		appMap[ac.Name] = ac
	}

	return &AppLauncher{
		conf: AppLauncherConfig{
			Apps:       apps,
			BinPath:    filepath.Join(dir, "bin"),
			LocalPath:  filepath.Join(dir, "local"),
			ServerAddr: ":0",
		},
		log:   testLogger(),
		procM: procM,
		apps:  appMap,
	}
}

func TestRegistry(t *testing.T) {
	called := false
	fn := func(_ context.Context, _ []string) error {
		called = true
		return nil
	}

	// Unknown app.
	_, ok := GetApp("registry-unknown-app")
	require.False(t, ok)

	// Register and retrieve.
	RegisterApp("registry-test-app", fn)
	got, ok := GetApp("registry-test-app")
	require.True(t, ok)
	require.NotNil(t, got)
	require.NoError(t, got(context.Background(), nil))
	require.True(t, called)

	// Re-registering the same name panics.
	require.Panics(t, func() {
		RegisterApp("registry-test-app", fn)
	})
}

func TestExpandHome(t *testing.T) {
	tests := []struct {
		name string
		in   string
		home string
		want string
	}{
		{"empty home is a no-op", "~/x", "", "~/x"},
		{"bare tilde", "~", "/home/u", "/home/u"},
		{"leading tilde slash", "~/.skycoin/wallets", "/home/u", "/home/u/.skycoin/wallets"},
		{"tilde-user is left alone", "~bob/x", "/home/u", "~bob/x"},
		{"no tilde unchanged", "/etc/passwd", "/home/u", "/etc/passwd"},
		{"tilde in middle unchanged", "a~/b", "/home/u", "a~/b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, expandHome(tc.in, tc.home))
		})
	}
}

func TestExpandHomeAll(t *testing.T) {
	// Empty home returns the input slice untouched.
	in := []string{"~/a", "b"}
	require.Equal(t, in, expandHomeAll(in, ""))

	// Empty args returns input untouched.
	require.Empty(t, expandHomeAll(nil, "/home/u"))

	out := expandHomeAll([]string{"~/a", "~", "plain"}, "/home/u")
	require.Equal(t, []string{"/home/u/a", "/home/u", "plain"}, out)

	// Input is not mutated in place.
	require.Equal(t, []string{"~/a", "b"}, in)
}

func TestExpandHomeAllEnv(t *testing.T) {
	require.Equal(t, []string(nil), expandHomeAllEnv(nil, "/home/u"))

	in := []string{"WALLET=~/.skycoin", "MALFORMED", "HOME=~"}
	out := expandHomeAllEnv(in, "/home/u")
	require.Equal(t, []string{"WALLET=/home/u/.skycoin", "MALFORMED", "HOME=/home/u"}, out)

	// Empty home leaves env untouched.
	require.Equal(t, in, expandHomeAllEnv(in, ""))
}

func TestEnvHasKey(t *testing.T) {
	env := []string{"FOO=1", "BAR=2", "EMPTY="}
	require.True(t, envHasKey(env, "FOO"))
	require.True(t, envHasKey(env, "EMPTY"))
	require.False(t, envHasKey(env, "BAZ"))
	require.False(t, envHasKey(env, "FO"))
}

func TestIsRawProcessApp(t *testing.T) {
	// Register an internal app for the registry-hit branches.
	noop := func(_ context.Context, _ []string) error { return nil }
	RegisterApp("rawtest-internal", noop)
	RegisterApp("rawtest-binary", noop)

	tests := []struct {
		name string
		ac   appserver.AppConfig
		want bool
	}{
		{
			name: "registry hit by name is not raw",
			ac:   appserver.AppConfig{Name: "rawtest-internal"},
			want: false,
		},
		{
			name: "registry hit by binary is not raw",
			ac:   appserver.AppConfig{Name: "rawtest-binary-2", Binary: "rawtest-binary"},
			want: false,
		},
		{
			name: "skywire app wrapper is not raw",
			ac:   appserver.AppConfig{Name: "vpn", Args: []string{"app", "vpn-client"}},
			want: false,
		},
		{
			name: "cobra subcommand is raw",
			ac:   appserver.AppConfig{Name: "skycoin", Args: []string{"skycoin", "daemon"}},
			want: true,
		},
		{
			name: "third-party binary with no args is raw",
			ac:   appserver.AppConfig{Name: "custom", Binary: "custom-bin"},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isRawProcessApp(tc.ac))
		})
	}
}

func TestEnsureDir(t *testing.T) {
	base := t.TempDir()

	// Creates a nested non-existent dir and absolutizes the path.
	p := filepath.Join(base, "a", "b")
	require.NoError(t, ensureDir(&p))
	require.True(t, filepath.IsAbs(p))
	info, err := os.Stat(p)
	require.NoError(t, err)
	require.True(t, info.IsDir())

	// Idempotent on an existing dir.
	require.NoError(t, ensureDir(&p))

	// Relative path gets absolutized.
	rel := "ensuredir-relative-test"
	defer os.RemoveAll(rel) //nolint:errcheck
	require.NoError(t, ensureDir(&rel))
	require.True(t, filepath.IsAbs(rel))
}

func TestMakeProcConfig(t *testing.T) {
	dir := t.TempDir()
	pk, _ := cipher.GenerateKeyPair()
	lc := AppLauncherConfig{
		VisorPK:    pk,
		ServerAddr: "1.2.3.4:5",
		BinPath:    filepath.Join(dir, "bin"),
		LocalPath:  filepath.Join(dir, "local"),
	}

	t.Run("defaults workdir, binary loc and merges env", func(t *testing.T) {
		ac := appserver.AppConfig{
			Name:   "myapp",
			Binary: "myapp-bin",
			Args:   []string{"--flag"},
			Env:    []string{"PERAPP=1"},
			Port:   42,
		}
		pc, err := makeProcConfig(lc, ac, []string{"BASE=0"})
		require.NoError(t, err)

		require.Equal(t, "myapp", pc.AppName)
		require.Equal(t, "1.2.3.4:5", pc.AppSrvAddr)
		require.Equal(t, pk, pc.VisorPK)
		require.Equal(t, filepath.Join(lc.LocalPath, "myapp"), pc.ProcWorkDir)
		require.Equal(t, filepath.Join(lc.BinPath, "myapp-bin"), pc.BinaryLoc)
		require.Equal(t, filepath.Join(lc.LocalPath, "myapp_log.db"), pc.LogDBLoc)
		require.False(t, pc.ProcKey.Null())
		// base env first, per-app env after (so per-app wins on dup keys).
		require.Contains(t, pc.ProcEnvs, "BASE=0")
		require.Contains(t, pc.ProcEnvs, "PERAPP=1")
		// No internal func: external binary stays.
		require.Nil(t, pc.RunFunc)
		// workdir created on disk.
		info, statErr := os.Stat(pc.ProcWorkDir)
		require.NoError(t, statErr)
		require.True(t, info.IsDir())
	})

	t.Run("honors WorkDir override", func(t *testing.T) {
		custom := filepath.Join(dir, "custom-wd")
		ac := appserver.AppConfig{Name: "wdapp", WorkDir: custom}
		pc, err := makeProcConfig(lc, ac, nil)
		require.NoError(t, err)
		require.Equal(t, custom, pc.ProcWorkDir)
	})

	t.Run("internal app by name clears nothing but sets RunFunc", func(t *testing.T) {
		RegisterApp("mpc-internal", func(_ context.Context, _ []string) error { return nil })
		ac := appserver.AppConfig{Name: "mpc-internal"} // Binary empty
		pc, err := makeProcConfig(lc, ac, nil)
		require.NoError(t, err)
		require.NotNil(t, pc.RunFunc)
	})

	t.Run("internal app by binary clears BinaryLoc", func(t *testing.T) {
		RegisterApp("mpc-binary", func(_ context.Context, _ []string) error { return nil })
		ac := appserver.AppConfig{Name: "mpc-binary-2", Binary: "mpc-binary"}
		pc, err := makeProcConfig(lc, ac, nil)
		require.NoError(t, err)
		require.NotNil(t, pc.RunFunc)
		require.Empty(t, pc.BinaryLoc)
	})
}

func TestResetConfig(t *testing.T) {
	l := newTestLauncher(t, &appserver.MockProcManager{},
		appserver.AppConfig{Name: "old"})

	pk, _ := cipher.GenerateKeyPair()
	l.ResetConfig(AppLauncherConfig{
		VisorPK:    pk,
		ServerAddr: "new:1",
		Apps:       []appserver.AppConfig{{Name: "new-a"}, {Name: "new-b"}},
	})

	require.Len(t, l.apps, 2)
	_, ok := l.apps["new-a"]
	require.True(t, ok)
	_, ok = l.apps["old"]
	require.False(t, ok)
	require.Equal(t, pk, l.conf.VisorPK)
	require.Equal(t, "new:1", l.conf.ServerAddr)
}

func TestAppState(t *testing.T) {
	t.Run("unknown app", func(t *testing.T) {
		l := newTestLauncher(t, &appserver.MockProcManager{})
		state, ok := l.AppState("nope")
		require.False(t, ok)
		require.Nil(t, state)
	})

	t.Run("stopped when no proc and no error", func(t *testing.T) {
		pm := &appserver.MockProcManager{}
		pm.On("ErrorByName", "app").Return("", false)
		pm.On("ProcByName", "app").Return((*appserver.Proc)(nil), false)

		l := newTestLauncher(t, pm, appserver.AppConfig{Name: "app"})
		state, ok := l.AppState("app")
		require.True(t, ok)
		require.Equal(t, appserver.AppStatusStopped, state.Status)
		require.Equal(t, "app", state.Name)
	})

	t.Run("errored from saved error with no proc", func(t *testing.T) {
		pm := &appserver.MockProcManager{}
		pm.On("ErrorByName", "app").Return("boom", true)
		pm.On("ProcByName", "app").Return((*appserver.Proc)(nil), false)

		l := newTestLauncher(t, pm, appserver.AppConfig{Name: "app"})
		state, ok := l.AppState("app")
		require.True(t, ok)
		require.Equal(t, appserver.AppStatusErrored, state.Status)
		require.Equal(t, "boom", state.DetailedStatus)
	})
}

func TestAppStates(t *testing.T) {
	pm := &appserver.MockProcManager{}
	pm.On("ErrorByName", mock.Anything).Return("", false)
	pm.On("ProcByName", mock.Anything).Return((*appserver.Proc)(nil), false)

	l := newTestLauncher(t, pm,
		appserver.AppConfig{Name: "a"},
		appserver.AppConfig{Name: "b"})

	states := l.AppStates()
	require.Len(t, states, 2)
	names := map[string]bool{}
	for _, s := range states {
		names[s.Name] = true
		require.Equal(t, appserver.AppStatusStopped, s.Status)
	}
	require.True(t, names["a"])
	require.True(t, names["b"])
}

func TestStartApp_NotFound(t *testing.T) {
	l := newTestLauncher(t, &appserver.MockProcManager{})
	err := l.StartApp("ghost", nil, nil)
	require.ErrorIs(t, err, ErrAppNotFound)
}

func TestStartApp_Success(t *testing.T) {
	pm := &appserver.MockProcManager{}
	pm.On("Start", mock.AnythingOfType("appcommon.ProcConfig")).
		Return(appcommon.ProcID(123), nil)

	l := newTestLauncher(t, pm, appserver.AppConfig{Name: "app", Binary: "app-bin"})
	require.NoError(t, l.StartApp("app", []string{"--x"}, []string{"E=1"}))

	// PID should have been persisted to the pid file.
	data, err := os.ReadFile(filepath.Join(l.conf.LocalPath, appsPIDFileName))
	require.NoError(t, err)
	require.Contains(t, string(data), "app 123")
	pm.AssertExpectations(t)
}

func TestStartApp_StartError(t *testing.T) {
	pm := &appserver.MockProcManager{}
	startErr := errors.New("start failed")
	pm.On("Start", mock.AnythingOfType("appcommon.ProcConfig")).
		Return(appcommon.ProcID(0), startErr)

	l := newTestLauncher(t, pm, appserver.AppConfig{Name: "app", Binary: "app-bin"})
	require.ErrorIs(t, l.StartApp("app", nil, nil), startErr)
}

func TestStartAppWithMode(t *testing.T) {
	t.Run("internal mode clears binary", func(t *testing.T) {
		pm := &appserver.MockProcManager{}
		var captured appcommon.ProcConfig
		pm.On("Start", mock.AnythingOfType("appcommon.ProcConfig")).
			Run(func(args mock.Arguments) {
				captured = args.Get(0).(appcommon.ProcConfig)
			}).Return(appcommon.ProcID(1), nil)

		l := newTestLauncher(t, pm, appserver.AppConfig{Name: "app", Binary: "app-bin"})
		require.NoError(t, l.StartAppWithMode("app", nil, nil, "internal"))
		// Binary cleared -> BinaryLoc points at BinPath joined with "".
		require.Equal(t, l.conf.BinPath, captured.BinaryLoc)
	})

	t.Run("external mode defaults binary to app name", func(t *testing.T) {
		pm := &appserver.MockProcManager{}
		var captured appcommon.ProcConfig
		pm.On("Start", mock.AnythingOfType("appcommon.ProcConfig")).
			Run(func(args mock.Arguments) {
				captured = args.Get(0).(appcommon.ProcConfig)
			}).Return(appcommon.ProcID(1), nil)

		l := newTestLauncher(t, pm, appserver.AppConfig{Name: "app"}) // no binary
		require.NoError(t, l.StartAppWithMode("app", nil, nil, "external"))
		require.Equal(t, filepath.Join(l.conf.BinPath, "app"), captured.BinaryLoc)
	})
}

func TestRegisterDeregisterApp(t *testing.T) {
	pm := &appserver.MockProcManager{}
	key := appcommon.RandProcKey()
	conf := appcommon.ProcConfig{AppName: "ext"}
	pm.On("Register", conf).Return(key, nil)
	pm.On("Deregister", key).Return(nil)

	l := newTestLauncher(t, pm)

	gotKey, err := l.RegisterApp(conf)
	require.NoError(t, err)
	require.Equal(t, key, gotKey)

	require.NoError(t, l.DeregisterApp(key))
	pm.AssertExpectations(t)
}

func TestStopApp(t *testing.T) {
	t.Run("not running", func(t *testing.T) {
		pm := &appserver.MockProcManager{}
		pm.On("ProcByName", "app").Return((*appserver.Proc)(nil), false)

		l := newTestLauncher(t, pm)
		_, err := l.StopApp("app")
		require.ErrorIs(t, err, ErrAppNotRunning)
	})

	t.Run("success", func(t *testing.T) {
		pm := &appserver.MockProcManager{}
		proc := &appserver.Proc{}
		pm.On("ProcByName", "app").Return(proc, true)
		pm.On("Stop", "app").Return(nil)

		l := newTestLauncher(t, pm)
		got, err := l.StopApp("app")
		require.NoError(t, err)
		require.Equal(t, proc, got)
		pm.AssertExpectations(t)
	})

	t.Run("stop error returns proc and error", func(t *testing.T) {
		pm := &appserver.MockProcManager{}
		proc := &appserver.Proc{}
		stopErr := errors.New("stop failed")
		pm.On("ProcByName", "app").Return(proc, true)
		pm.On("Stop", "app").Return(stopErr)

		l := newTestLauncher(t, pm)
		got, err := l.StopApp("app")
		require.ErrorIs(t, err, stopErr)
		require.Equal(t, proc, got)
	})
}

func TestRestartApp_StopFails(t *testing.T) {
	pm := &appserver.MockProcManager{}
	pm.On("ProcByName", "app").Return((*appserver.Proc)(nil), false)

	l := newTestLauncher(t, pm)
	err := l.RestartApp("app", "app-bin")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAppNotRunning)
}

func TestAutoStart(t *testing.T) {
	t.Run("starts only auto-start apps", func(t *testing.T) {
		pm := &appserver.MockProcManager{}
		// only the autostart app reaches Start.
		pm.On("Start", mock.MatchedBy(func(c appcommon.ProcConfig) bool {
			return c.AppName == "auto"
		})).Return(appcommon.ProcID(7), nil)

		l := newTestLauncher(t, pm,
			appserver.AppConfig{Name: "auto", Binary: "auto-bin", AutoStart: true},
			appserver.AppConfig{Name: "manual", Binary: "manual-bin", AutoStart: false})

		require.NoError(t, l.AutoStart(nil))
		pm.AssertExpectations(t)
	})

	t.Run("env maker error aborts", func(t *testing.T) {
		pm := &appserver.MockProcManager{}
		l := newTestLauncher(t, pm,
			appserver.AppConfig{Name: "auto", Binary: "auto-bin", AutoStart: true})

		envErr := errors.New("env boom")
		err := l.AutoStart(EnvMap{
			"auto": func() ([]string, error) { return nil, envErr },
		})
		require.ErrorIs(t, err, envErr)
	})
}

func TestKillApp(t *testing.T) {
	pm := &appserver.MockProcManager{}
	l := newTestLauncher(t, pm)
	require.NoError(t, ensureDir(&l.conf.LocalPath))

	// Seed the pid file with an entry for our app and an unrelated app.
	pidPath := filepath.Join(l.conf.LocalPath, appsPIDFileName)
	require.NoError(t, os.WriteFile(pidPath, []byte("myapp 999999\nother 888888\n"), 0o600))

	// killApp scans the file and signals the matching pid. pid 999999 is
	// almost certainly not a live process, so Signal fails silently and
	// the call returns without error.
	require.NoError(t, l.KillApp("myapp"))
}

func TestKillHangingProcesses(t *testing.T) {
	pm := &appserver.MockProcManager{}
	l := newTestLauncher(t, pm)
	require.NoError(t, ensureDir(&l.conf.LocalPath))

	pidPath := filepath.Join(l.conf.LocalPath, appsPIDFileName)
	require.NoError(t, os.WriteFile(pidPath, []byte("myapp 999999\n"), 0o600))

	require.NoError(t, l.killHangingProcesses())

	// File is emptied afterwards.
	data, err := os.ReadFile(pidPath)
	require.NoError(t, err)
	require.Empty(t, data)
}

func TestNewLauncher(t *testing.T) {
	defer appnet.ClearNetworkers()

	dir := t.TempDir()
	pm := &appserver.MockProcManager{}
	pk, _ := cipher.GenerateKeyPair()

	conf := AppLauncherConfig{
		VisorPK:   pk,
		BinPath:   filepath.Join(dir, "bin"),
		LocalPath: filepath.Join(dir, "local"),
		Apps: []appserver.AppConfig{
			{Name: "a", AutoStart: false},
		},
	}

	l, err := NewLauncher(testLogger(), conf, nil, nil, pm)
	require.NoError(t, err)
	require.NotNil(t, l)

	// Directories created and absolutized.
	require.True(t, filepath.IsAbs(l.conf.BinPath))
	for _, p := range []string{l.conf.BinPath, l.conf.LocalPath} {
		info, statErr := os.Stat(p)
		require.NoError(t, statErr)
		require.True(t, info.IsDir())
	}

	// Apps map populated.
	require.Len(t, l.apps, 1)
	_, ok := l.apps["a"]
	require.True(t, ok)

	// Networkers were registered.
	_, err = appnet.ResolveNetworker(appnet.TypeSkynet)
	require.NoError(t, err)
	_, err = appnet.ResolveNetworker(appnet.TypeDmsg)
	require.NoError(t, err)
}
