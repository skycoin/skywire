//go:build unix

// Package vpn pkg/vpn/tun_utun_fd.go c4-app-vpn
package vpn

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// The utun framing of Apple's kernel: every packet on a utun descriptor
// starts with the address family as a big-endian uint32, the layout
// wireguard-go's tun_darwin.go writes. AF_INET6 is Darwin's 30, not Linux's
// 10. iOS hands the packet-tunnel extension such a descriptor
// (tun_device_ios.go); this file builds on every unix so the framing is
// tested on any of them (TestIOSTunDevice).
const (
	utunHeaderLen = 4
	utunAFInet    = 2
	utunAFInet6   = 30
)

var errUtunShortPacket = errors.New("utun: packet shorter than its header")

// utunDevice is a TUNDevice over a utun descriptor: Read strips the header,
// Write adds it.
type utunDevice struct {
	file *os.File
	name string

	rmu  sync.Mutex
	rbuf []byte
	wmu  sync.Mutex
	wbuf []byte
}

func newUtunDevice(file *os.File, name string) *utunDevice {
	return &utunDevice{file: file, name: name}
}

// Name implements TUNDevice.
func (d *utunDevice) Name() string { return d.name }

// Read implements TUNDevice: one packet, without its header.
func (d *utunDevice) Read(p []byte) (int, error) {
	d.rmu.Lock()
	defer d.rmu.Unlock()
	if need := len(p) + utunHeaderLen; cap(d.rbuf) < need {
		d.rbuf = make([]byte, need)
	}
	buf := d.rbuf[:len(p)+utunHeaderLen]
	n, err := d.file.Read(buf)
	if n < utunHeaderLen {
		if err == nil {
			err = errUtunShortPacket
		}
		return 0, err
	}
	return copy(p, buf[utunHeaderLen:n]), err
}

// Write implements TUNDevice: one IP packet, framed by its version.
func (d *utunDevice) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, io.ErrShortWrite
	}
	var family byte
	switch p[0] >> 4 {
	case 4:
		family = utunAFInet
	case 6:
		family = utunAFInet6
	default:
		return 0, fmt.Errorf("utun: not an IP packet (version %d)", p[0]>>4)
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if need := len(p) + utunHeaderLen; cap(d.wbuf) < need {
		d.wbuf = make([]byte, need)
	}
	buf := d.wbuf[:len(p)+utunHeaderLen]
	buf[0], buf[1], buf[2], buf[3] = 0, 0, 0, family
	copy(buf[utunHeaderLen:], p)
	n, err := d.file.Write(buf)
	if n < utunHeaderLen {
		return 0, err
	}
	return n - utunHeaderLen, err
}

// Close implements TUNDevice. It closes this device's own descriptor, which
// unblocks a pending Read.
func (d *utunDevice) Close() error {
	return d.file.Close()
}
