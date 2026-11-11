package visor

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	skycall "github.com/skycoin/skywire/pkg/skychat/call"
)

// A device that fails to open still gives the call something to run on, and
// keeps the reason so the UI can tell the user.
func TestVoiceDeviceFallbackKeepsTheReason(t *testing.T) {
	log := logging.MustGetLogger("test")
	noServer := errors.New("voice: pulse connect: no such file or directory")

	src := micOrSilence(func() (skycall.Source, error) { return nil, noServer }, log)
	require.Equal(t, skycall.UnavailableSource{Err: noServer}, src)

	sink := speakerOrNull(func() (skycall.Sink, error) { return nil, noServer }, log)
	require.Equal(t, skycall.UnavailableSink{Err: noServer}, sink)

	real := skycall.NullSink{}
	require.Equal(t, real, speakerOrNull(func() (skycall.Sink, error) { return real, nil }, log))
}

func TestVoiceCallDevicesWithoutVoice(t *testing.T) {
	_, err := (&Visor{}).VoiceCallDevices("x")
	require.ErrorIs(t, err, ErrVoiceDisabled)
}
