package dmsgserver

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAsyncConfigSection(t *testing.T) {
	var c Config
	require.NoError(t, json.Unmarshal([]byte(`{}`), &c))
	got, err := c.Async.StoreConfig("/etc/skywire")
	require.NoError(t, err)
	require.Nil(t, got, "absent is off")

	got, err = (&AsyncConfig{Enable: false, TTL: "1h"}).StoreConfig("")
	require.NoError(t, err)
	require.Nil(t, got, "enable false is off")

	require.NoError(t, json.Unmarshal([]byte(`{"async":{"enable":true,"dir":"async","max_message_size":2048,
		"max_per_recipient":5,"max_recipient_bytes":4096,"max_sender_per_hour":10,"max_total_size":-1,"ttl":"48h"}}`), &c))
	got, err = c.Async.StoreConfig("/etc/skywire")
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/etc/skywire", "async"), got.Dir, "relative to the config file")
	require.Equal(t, 2048, got.MaxMessage)
	require.Equal(t, 5, got.MaxPerRecipient)
	require.Equal(t, int64(4096), got.MaxRecipientBytes)
	require.Equal(t, 10, got.MaxSenderPerHour)
	require.Equal(t, int64(-1), got.MaxTotal)
	require.Equal(t, 48*time.Hour, got.TTL)

	got, err = (&AsyncConfig{Enable: true, Dir: "/var/lib/async"}).StoreConfig("/etc/skywire")
	require.NoError(t, err)
	require.Equal(t, "/var/lib/async", got.Dir, "an absolute dir stays")
	require.Zero(t, got.TTL, "zero takes the default in the store")

	_, err = (&AsyncConfig{Enable: true, TTL: "three days"}).StoreConfig("")
	require.Error(t, err)
}
