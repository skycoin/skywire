//go:build !(js && wasm) && !tinygo

package rewards

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func block(t *testing.T, fields map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(fields)
	require.NoError(t, err)
	return raw
}

func TestFactory(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	_, err := factory(block(t, map[string]any{"secret_key": sk.Hex()}), nil)
	require.ErrorContains(t, err, "work_dir")
	_, err = factory(block(t, map[string]any{"work_dir": t.TempDir()}), nil)
	require.ErrorContains(t, err, "secret_key")

	svc, err := factory(block(t, map[string]any{"secret_key": sk.Hex(), "work_dir": "/no/such/dir", "no_cycle": true}), nil)
	require.NoError(t, err)
	require.ErrorContains(t, svc.Run(context.Background()), "not a directory")
}

func TestBotCredentials(t *testing.T) {
	t.Setenv("TG_BOT_TOKEN", "")
	t.Setenv("TG_CHAT_ID", "")
	s := &service{}
	_, _, err := s.botCredentials()
	require.Error(t, err)

	t.Setenv("TG_BOT_TOKEN", "env-token")
	t.Setenv("TG_CHAT_ID", "42")
	token, chat, err := s.botCredentials()
	require.NoError(t, err)
	require.Equal(t, "env-token", token)
	require.EqualValues(t, 42, chat)

	// The file and the block win over the environment.
	f := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(f, []byte("file-token\n"), 0o600))
	s.cfg = Config{BotTokenFile: f, BotChatID: -100}
	token, chat, err = s.botCredentials()
	require.NoError(t, err)
	require.Equal(t, "file-token", token)
	require.EqualValues(t, -100, chat)
}

func TestStateHasNoSecrets(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	f := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(f, []byte("secret-token"), 0o600))
	svc, err := factory(block(t, map[string]any{"secret_key": sk.Hex(), "work_dir": "/srv/rewards", "bot_token_file": f}), nil)
	require.NoError(t, err)
	st := svc.(*service).State()
	require.Equal(t, "files", st.Store)
	out, err := json.Marshal(st)
	require.NoError(t, err)
	require.Contains(t, string(out), `"work_dir":"/srv/rewards"`)
	require.NotContains(t, string(out), sk.Hex())
	require.NotContains(t, string(out), "secret-token")
}
