package visorconfig

import (
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
)

// The visor adds apps such as skymail to the launcher at runtime, outside the
// config's list. An autostart change must not drop them, and must reach them.
func TestUpdateAppAutostartKeepsRuntimeApps(t *testing.T) {
	dir := t.TempDir()
	_, sk := cipher.GenerateKeyPair()
	cc, err := NewCommon(nil, filepath.Join(dir, "config.json"), &sk)
	require.NoError(t, err)
	conf := MakeBaseConfig(cc, true, nil, nil)
	conf.Launcher.Apps = []appserver.AppConfig{{Name: "skychat"}}

	pm := &appserver.MockProcManager{}
	pm.On("ErrorByName", mock.Anything).Return("", false)
	pm.On("ProcByName", mock.Anything).Return((*appserver.Proc)(nil), false)
	l, err := launcher.NewLauncher(logrus.New(), launcher.AppLauncherConfig{
		BinPath:   filepath.Join(dir, "bin"),
		LocalPath: filepath.Join(dir, "local"),
		Apps:      []appserver.AppConfig{{Name: "skychat"}, {Name: "skymail"}},
	}, nil, nil, pm)
	require.NoError(t, err)

	require.NoError(t, conf.UpdateAppAutostart(l, "skychat", true))
	st, ok := l.AppState("skymail")
	require.True(t, ok, "a runtime app stays listed")
	require.False(t, st.AutoStart)
	st, _ = l.AppState("skychat")
	require.True(t, st.AutoStart)
	require.True(t, conf.Launcher.Apps[0].AutoStart)

	require.NoError(t, conf.UpdateAppAutostart(l, "skymail", true))
	st, _ = l.AppState("skymail")
	require.True(t, st.AutoStart, "a runtime app's autostart changes in the launcher")
}
