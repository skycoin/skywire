package visor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestAsyncSettings(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	var s asyncSettings
	for k, v := range map[string]string{
		"host": "false", "hosted": a.Hex() + "," + b.Hex(), "max_message": "32KiB",
		"max_total_size": "1MiB", "ttl": "3d", "inbox": "false", "inbox_hosts": a.Hex(),
		"publish": "false", "allow": "none",
	} {
		require.NoError(t, s.set(k, v), k)
	}
	require.Len(t, s.keys(), 9)

	off := true
	base := visorconfig.DmsgAsyncConfig{
		Host:  visorconfig.DmsgAsyncHostConfig{Enable: &off, Store: "memory", MaxMessage: 1000},
		Inbox: visorconfig.DmsgAsyncInboxConfig{Allow: []cipher.PubKey{b}},
	}
	c := s.over(base)
	require.False(t, *c.Host.Enable)
	require.Equal(t, "memory", c.Host.Store, "a key the settings do not have keeps the config's value")
	require.Equal(t, []cipher.PubKey{a, b}, c.Host.Hosted)
	require.Equal(t, 32<<10, c.Host.MaxMessage)
	require.EqualValues(t, 1<<20, c.Host.MaxTotalSize)
	require.Equal(t, visorconfig.Duration(72*time.Hour), c.Host.TTL)
	require.True(t, c.Inbox.Disable)
	require.Equal(t, []cipher.PubKey{a}, c.Inbox.Hosts)
	require.False(t, *c.Inbox.Publish)
	require.Empty(t, c.Inbox.Allow, "none empties the list, which allows every key")
	require.True(t, *base.Host.Enable, "over does not change the config")

	dir := t.TempDir()
	require.NoError(t, s.save(dir))
	got, err := loadAsyncSettings(dir)
	require.NoError(t, err)
	require.Equal(t, s, got)

	require.NoError(t, s.set("max_message", "default"))
	require.NoError(t, s.set("host", "default"))
	require.Equal(t, 1000, s.over(base).Host.MaxMessage, "default returns to the config")
	require.True(t, *s.over(base).Host.Enable)

	require.Error(t, s.set("max_message", "2MiB"), "over the protocol limit")
	require.Error(t, s.set("inbox", "maybe"))
	require.Error(t, s.set("hosted", "nope"))
	require.Error(t, s.set("speed", "fast"))

	empty, err := loadAsyncSettings(t.TempDir())
	require.NoError(t, err, "no file is no settings")
	require.Empty(t, empty.keys())
}
