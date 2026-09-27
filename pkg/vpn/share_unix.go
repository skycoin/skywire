//go:build unix

// Package vpn pkg/vpn/share_unix.go c4-app-vpn
package vpn

import (
	"io"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// shareMaxFDs bounds the descriptors one message may carry. The app sends one
// at a time; the room for more only keeps a misbehaving sender's extras from
// being truncated away (and leaked) instead of closed.
const shareMaxFDs = 8

// receiveShared reads handed-over connections off uc until it ends. Each one
// arrives as a descriptor in SCM_RIGHTS ancillary data riding a single byte —
// the same handoff the TUN itself takes (tun_device_android.go) — and is
// passed to push as a net.Conn of its own.
func receiveShared(uc *net.UnixConn, push func(net.Conn) bool) error {
	buf := make([]byte, 1)
	oob := make([]byte, unix.CmsgSpace(4*shareMaxFDs))
	for {
		n, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
		if err != nil {
			return err
		}
		if n == 0 && oobn == 0 {
			return io.EOF
		}
		msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			continue
		}
		for i := range msgs {
			fds, err := unix.ParseUnixRights(&msgs[i])
			if err != nil {
				continue
			}
			for _, fd := range fds {
				conn, err := fdConn(fd)
				if err != nil {
					continue
				}
				push(conn)
			}
		}
	}
}

// fdConn adopts a received socket descriptor as a net.Conn. FileConn takes a
// duplicate, so the original is closed here either way.
func fdConn(fd int) (net.Conn, error) {
	f := os.NewFile(uintptr(fd), "shared")
	defer f.Close() //nolint:errcheck
	return net.FileConn(f)
}
