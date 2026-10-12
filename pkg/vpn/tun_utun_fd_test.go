//go:build unix

// Package vpn pkg/vpn/tun_utun_fd_test.go c4-app-vpn
package vpn

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// utunPair is a utunDevice and the kernel's end of it: a datagram socket
// pair keeps packet boundaries, as a utun descriptor does.
func utunPair(t *testing.T) (*utunDevice, *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	require.NoError(t, err)
	for _, fd := range fds {
		require.NoError(t, syscall.SetNonblock(fd, true))
	}
	dev := newUtunDevice(os.NewFile(uintptr(fds[0]), "utun7"), "utun7")
	kernel := os.NewFile(uintptr(fds[1]), "kernel")
	t.Cleanup(func() {
		_ = dev.Close()    //nolint:errcheck
		_ = kernel.Close() //nolint:errcheck
	})
	return dev, kernel
}

// Minimal headers: only the version nibble matters to the framing.
var (
	utunIPv4 = append([]byte{0x45, 0x00, 0x00, 0x1c}, make([]byte, 24)...)
	utunIPv6 = append([]byte{0x60, 0x00, 0x00, 0x00}, make([]byte, 36)...)
)

// TestIOSTunDevice is the iOS TUN device's framing on any host: packets from
// the kernel arrive without utun's 4-byte family header, and packets the
// client writes go out with it (AF_INET 2, Darwin's AF_INET6 30, big-endian).
func TestIOSTunDevice(t *testing.T) {
	dev, kernel := utunPair(t)
	require.Equal(t, "utun7", dev.Name())

	for _, c := range []struct {
		name   string
		family byte
		packet []byte
	}{
		{"ipv4", 2, utunIPv4},
		{"ipv6", 30, utunIPv6},
	} {
		t.Run(c.name, func(t *testing.T) {
			framed := append([]byte{0, 0, 0, c.family}, c.packet...)
			_, err := kernel.Write(framed)
			require.NoError(t, err)
			buf := make([]byte, 1500)
			n, err := dev.Read(buf)
			require.NoError(t, err)
			require.Equal(t, c.packet, buf[:n], "the header was not stripped")

			// The client's read loop writes what it got back: framed again.
			n, err = dev.Write(buf[:n])
			require.NoError(t, err)
			require.Equal(t, len(c.packet), n)
			out := make([]byte, 1500)
			m, err := kernel.Read(out)
			require.NoError(t, err)
			require.Equal(t, framed, out[:m])
		})
	}
}

func TestIOSTunDeviceRefusesWhatIsNotIP(t *testing.T) {
	dev, kernel := utunPair(t)
	_, err := dev.Write([]byte{0x10, 0, 0, 0})
	require.Error(t, err)
	_, err = dev.Write(nil)
	require.Error(t, err)

	_, err = kernel.Write([]byte{0, 0})
	require.NoError(t, err)
	_, err = dev.Read(make([]byte, 64))
	require.ErrorIs(t, err, errUtunShortPacket)
}

// Close unblocks a Read waiting for a packet, as stopping the client needs.
func TestIOSTunDeviceCloseUnblocksRead(t *testing.T) {
	dev, _ := utunPair(t)
	done := make(chan error, 1)
	go func() {
		_, err := dev.Read(make([]byte, 64))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, dev.Close())
	select {
	case err := <-done:
		require.True(t, errors.Is(err, os.ErrClosed), "read ended with %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Close left the Read blocked")
	}
}
