package visor

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// Frames between the relay and the desk round-trip, and one past the size
// limit is refused before its payload is allocated.
func TestBrowseWSFrames(t *testing.T) {
	var b bytes.Buffer
	require.NoError(t, writeBrowseWSFrame(&b, browseWSText, []byte("hello")))
	require.NoError(t, writeBrowseWSFrame(&b, browseWSBinary, []byte{0, 1, 250}))
	require.NoError(t, writeBrowseWSFrame(&b, browseWSOpen, nil))

	for _, want := range []struct {
		t byte
		p []byte
	}{{browseWSText, []byte("hello")}, {browseWSBinary, []byte{0, 1, 250}}, {browseWSOpen, []byte{}}} {
		ty, p, err := readBrowseWSFrame(&b)
		require.NoError(t, err)
		require.Equal(t, want.t, ty)
		require.Equal(t, want.p, p)
	}

	hdr := make([]byte, 5)
	hdr[0] = browseWSBinary
	binary.BigEndian.PutUint32(hdr[1:], browseWSMaxMessage+1)
	_, _, err := readBrowseWSFrame(bytes.NewReader(hdr))
	require.Error(t, err)
}
