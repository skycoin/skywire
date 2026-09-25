package visorapi

import (
	"bytes"
	"encoding/gob"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMailSettingsSurviveGob: false and 0 are the values gob would drop
// from a pointer, and exactly the ones that mean "off" and "default".
func TestMailSettingsSurviveGob(t *testing.T) {
	off, zero, none, age := false, int64(0), int64(-1), time.Duration(0)
	in := MailSettingsUpdate{Enable: &off, MaxMessageSize: &zero, MaxTotalSize: &none, MaxAge: &age}

	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(in.Wire()))
	var w MailSettingsWire
	require.NoError(t, gob.NewDecoder(&buf).Decode(&w))
	out := w.Update()

	require.NotNil(t, out.Enable)
	require.False(t, *out.Enable)
	require.NotNil(t, out.MaxMessageSize)
	require.Zero(t, *out.MaxMessageSize)
	require.Equal(t, int64(-1), *out.MaxTotalSize)
	require.NotNil(t, out.MaxAge)

	buf.Reset()
	require.NoError(t, gob.NewEncoder(&buf).Encode(MailSettingsUpdate{}.Wire()))
	w = MailSettingsWire{}
	require.NoError(t, gob.NewDecoder(&buf).Decode(&w))
	require.Equal(t, MailSettingsUpdate{}, w.Update(), "unset stays unset")
}
