//+build windows

package vpn

import "golang.zx2c4.com/wireguard/tun"

type tunDevice struct {
	tun  tun.Device
	name string
}

func (t *tunDevice) Read(buf []byte) (int, error) {
	return t.tun.Read(buf, 0)
}

func (t *tunDevice) Write(buf []byte) (int, error) {
	return t.tun.Write(buf, 0)
}

func (t *tunDevice) Close() error {
	return t.tun.Close()
}

func (t *tunDevice) Name() string {
	return t.name
}
