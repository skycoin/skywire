//go:build windows

package vpn

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.zx2c4.com/wireguard/tun"
)

// fakeTUN records written packets and, like the wintun device, reports a
// count of packets rather than bytes.
type fakeTUN struct{ got [][]byte }

func (f *fakeTUN) File() *os.File                         { return nil }
func (f *fakeTUN) Read([][]byte, []int, int) (int, error) { return 0, io.EOF }
func (f *fakeTUN) Write(bufs [][]byte, _ int) (int, error) {
	for _, b := range bufs {
		f.got = append(f.got, append([]byte(nil), b...))
	}
	return len(bufs), nil
}
func (f *fakeTUN) MTU() (int, error)        { return TUNMTU, nil }
func (f *fakeTUN) Name() (string, error)    { return "tun0", nil }
func (f *fakeTUN) Events() <-chan tun.Event { return nil }
func (f *fakeTUN) Close() error             { return nil }
func (f *fakeTUN) BatchSize() int           { return 1 }

// io.Copy into the device must not stop with a short write.
func TestTUNDeviceWriteReportsBytes(t *testing.T) {
	f := &fakeTUN{}
	d := &tunDevice{tun: f, name: "tun0"}
	pkt := bytes.Repeat([]byte{0x45}, 84)
	n, err := io.Copy(d, bytes.NewReader(pkt))
	require.NoError(t, err)
	require.EqualValues(t, len(pkt), n)
	require.Len(t, f.got, 1)
}
