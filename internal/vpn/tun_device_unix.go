//+build !windows

package vpn

import (
	"github.com/songgao/water"
)

type tunDevice struct {
	tun *water.Interface
}

func (t *tunDevice) Read(buf []byte) (int, error) {
	return t.tun.Read(buf)
}

func (t *tunDevice) Write(buf []byte) (int, error) {
	return t.tun.Write(buf)
}

func (t *tunDevice) Close() error {
	return t.tun.Close()
}

func (t *tunDevice) Name() string {
	return t.tun.Name()
}
