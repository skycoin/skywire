//go:build linux

// Package hostbridge pkg/hostbridge/tun_linux.go c3-vis-wasm
package hostbridge

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strconv"
	"syscall"
	"unsafe"
)

// The TUN device and its addresses and routes, set up the way the native
// vpn-client does it, over netlink. Both need CAP_NET_ADMIN, which the
// bridge has when it runs as root or with that capability.
const (
	tunSetIff  = 0x400454ca // TUNSETIFF, linux/if_tun.h
	iffTUN     = 0x0001
	iffNoPI    = 0x1000
	ifNameSize = 16
	ifReqSize  = 40
)

// tunDevice reads with select on the device and a wake pipe, since closing
// the fd does not wake a read blocked on it.
type tunDevice struct {
	fd   int
	wake [2]int
	name string
}

func openTUN() (*tunDevice, error) {
	fd, err := syscall.Open("/dev/net/tun", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var ifr [ifReqSize]byte
	binary.NativeEndian.PutUint16(ifr[ifNameSize:], iffTUN|iffNoPI)
	if err := ioctl(fd, tunSetIff, &ifr); err != nil {
		_ = syscall.Close(fd) //nolint:errcheck
		return nil, err
	}
	t := &tunDevice{fd: fd, name: cstr(ifr[:ifNameSize])}
	if err := syscall.Pipe2(t.wake[:], syscall.O_CLOEXEC); err != nil {
		_ = syscall.Close(fd) //nolint:errcheck
		return nil, err
	}
	return t, nil
}

func (t *tunDevice) Name() string { return t.name }

func (t *tunDevice) Read(p []byte) (int, error) {
	for {
		var r syscall.FdSet
		fdSet(&r, t.fd)
		fdSet(&r, t.wake[0])
		n := max(t.fd, t.wake[0]) + 1
		if _, err := syscall.Select(n, &r, nil, nil, nil); err != nil {
			if err == syscall.EINTR {
				continue
			}
			return 0, err
		}
		if fdIsSet(&r, t.wake[0]) {
			return 0, errors.New("hostbridge: tun closed")
		}
		m, err := syscall.Read(t.fd, p)
		if err == syscall.EINTR || err == syscall.EAGAIN {
			continue
		}
		return m, err
	}
}

func (t *tunDevice) Write(p []byte) (int, error) { return syscall.Write(t.fd, p) }

// Close wakes the reader. close releases the device once it has returned.
func (t *tunDevice) Close() error {
	_, err := syscall.Write(t.wake[1], []byte{0})
	return err
}

func (t *tunDevice) release() {
	_ = syscall.Close(t.fd)      //nolint:errcheck
	_ = syscall.Close(t.wake[0]) //nolint:errcheck
	_ = syscall.Close(t.wake[1]) //nolint:errcheck
}

func fdSet(s *syscall.FdSet, fd int) {
	bits := 8 * int(unsafe.Sizeof(s.Bits[0]))
	s.Bits[fd/bits] |= 1 << (uint(fd) % uint(bits))
}

func fdIsSet(s *syscall.FdSet, fd int) bool {
	bits := 8 * int(unsafe.Sizeof(s.Bits[0]))
	return s.Bits[fd/bits]&(1<<(uint(fd)%uint(bits))) != 0
}

func ioctl(fd int, req uintptr, ifr *[ifReqSize]byte) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&ifr[0]))) //nolint:gosec // ioctl takes the ifreq by pointer
	if e != 0 {
		return e
	}
	return nil
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func ifIndex(name string) (uint32, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	defer syscall.Close(fd) //nolint:errcheck
	var ifr [ifReqSize]byte
	copy(ifr[:ifNameSize-1], name)
	if err := ioctl(fd, syscall.SIOCGIFINDEX, &ifr); err != nil {
		return 0, err
	}
	return binary.NativeEndian.Uint32(ifr[ifNameSize:]), nil
}

// tunSetup gives the device its address and MTU and brings it up, as
// `ip addr add`, `ip link set mtu` and `ip link set up` do.
func tunSetup(name, cidr string, mtu int) error {
	idx, err := ifIndex(name)
	if err != nil {
		return err
	}
	if mtu < 68 || mtu > 65535 {
		return errors.New("hostbridge: bad mtu " + strconv.Itoa(mtu))
	}
	p, err := netip.ParsePrefix(cidr)
	if err != nil || !p.Addr().Is4() {
		return errors.New("hostbridge: bad tun address " + cidr)
	}
	a := p.Addr().As4()
	bits := byte(p.Bits()) //nolint:gosec // at most 32 for IPv4
	addr := []byte{syscall.AF_INET, bits, 0, 0}
	addr = binary.NativeEndian.AppendUint32(addr, idx)
	addr = rtAttr(addr, syscall.IFA_LOCAL, a[:])
	addr = rtAttr(addr, syscall.IFA_ADDRESS, a[:])
	err = netlink(syscall.RTM_NEWADDR, syscall.NLM_F_CREATE|syscall.NLM_F_EXCL, addr, nil)
	if err != nil && err != syscall.EEXIST {
		return err
	}
	link := []byte{syscall.AF_UNSPEC, 0, 0, 0}
	link = binary.NativeEndian.AppendUint32(link, idx)
	link = binary.NativeEndian.AppendUint32(link, syscall.IFF_UP)
	link = binary.NativeEndian.AppendUint32(link, syscall.IFF_UP)
	link = rtAttr(link, syscall.IFLA_MTU, binary.NativeEndian.AppendUint32(nil, uint32(mtu))) //nolint:gosec // checked above
	return netlink(syscall.RTM_NEWLINK, 0, link, nil)
}

// route adds, changes or deletes a route to dst via gw in the main table,
// as `ip route add|replace|del` do.
func route(op, dst, gw string) error {
	p, err := netip.ParsePrefix(dst)
	if err != nil {
		a, aerr := netip.ParseAddr(dst)
		if aerr != nil {
			return errors.New("hostbridge: bad route destination " + dst)
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	g, err := netip.ParseAddr(gw)
	if err != nil || !g.Is4() || !p.Addr().Is4() {
		return errors.New("hostbridge: bad route " + dst + " via " + gw)
	}
	typ, flags := uint16(syscall.RTM_NEWROUTE), uint16(0)
	proto, scope, kind := byte(syscall.RTPROT_BOOT), byte(syscall.RT_SCOPE_UNIVERSE), byte(syscall.RTN_UNICAST)
	switch op {
	case "add":
		flags = syscall.NLM_F_CREATE | syscall.NLM_F_EXCL
	case "change":
		flags = syscall.NLM_F_CREATE | syscall.NLM_F_REPLACE
	case "del":
		typ, proto, scope, kind = syscall.RTM_DELROUTE, 0, syscall.RT_SCOPE_NOWHERE, 0
	default:
		return errors.New("hostbridge: bad route op " + op)
	}
	bits := byte(p.Bits()) //nolint:gosec // at most 32 for IPv4
	m := []byte{syscall.AF_INET, bits, 0, 0, syscall.RT_TABLE_MAIN, proto, scope, kind, 0, 0, 0, 0}
	if p.Bits() > 0 {
		d := p.Masked().Addr().As4()
		m = rtAttr(m, syscall.RTA_DST, d[:])
	}
	ga := g.As4()
	m = rtAttr(m, syscall.RTA_GATEWAY, ga[:])
	err = netlink(typ, flags, m, nil)
	if op == "add" && err == syscall.EEXIST {
		return nil
	}
	return err
}

// defaultGateway is the gateway of the main table's IPv4 default route.
func defaultGateway() (string, error) {
	var gw string
	m := []byte{syscall.AF_INET, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	err := netlink(syscall.RTM_GETROUTE, syscall.NLM_F_DUMP, m, func(typ uint16, b []byte) {
		if gw != "" || typ != syscall.RTM_NEWROUTE || len(b) < 12 || b[1] != 0 || b[4] != syscall.RT_TABLE_MAIN {
			return
		}
		for a := b[12:]; len(a) >= 4; {
			l := int(binary.NativeEndian.Uint16(a))
			if l < 4 || l > len(a) {
				return
			}
			if binary.NativeEndian.Uint16(a[2:]) == syscall.RTA_GATEWAY && l == 8 {
				gw = netip.AddrFrom4([4]byte(a[4:8])).String()
			}
			a = a[min(align4(l), len(a)):]
		}
	})
	if err == nil && gw == "" {
		err = errors.New("hostbridge: no default gateway")
	}
	return gw, err
}

func align4(n int) int { return (n + 3) &^ 3 }

func rtAttr(b []byte, typ uint16, data []byte) []byte {
	b = binary.NativeEndian.AppendUint16(b, uint16(4+len(data))) //nolint:gosec // attributes here are a few bytes
	b = binary.NativeEndian.AppendUint16(b, typ)
	b = append(b, data...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// netlink sends one rtnetlink request and reads to its ack or, for a dump,
// its end, passing each message on to each.
func netlink(typ, flags uint16, body []byte, each func(uint16, []byte)) error {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return err
	}
	defer syscall.Close(fd) //nolint:errcheck
	sa := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}
	if err := syscall.Bind(fd, sa); err != nil {
		return err
	}
	// NLM_F_EXCL shares a bit with NLM_F_DUMP, so each marks a dump.
	if each == nil {
		flags |= syscall.NLM_F_ACK
	}
	msg := binary.NativeEndian.AppendUint32(nil, uint32(syscall.NLMSG_HDRLEN+len(body))) //nolint:gosec // one small request
	msg = binary.NativeEndian.AppendUint16(msg, typ)
	msg = binary.NativeEndian.AppendUint16(msg, syscall.NLM_F_REQUEST|flags)
	msg = binary.NativeEndian.AppendUint32(msg, 1)
	msg = binary.NativeEndian.AppendUint32(msg, 0)
	msg = append(msg, body...)
	if err := syscall.Sendto(fd, msg, 0, sa); err != nil {
		return err
	}
	buf := make([]byte, 1<<16)
	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		for b := buf[:n]; len(b) >= syscall.NLMSG_HDRLEN; {
			l := int(binary.NativeEndian.Uint32(b))
			if l < syscall.NLMSG_HDRLEN || l > len(b) {
				return errors.New("hostbridge: bad netlink reply")
			}
			switch t := binary.NativeEndian.Uint16(b[4:]); t {
			case syscall.NLMSG_DONE:
				return nil
			case syscall.NLMSG_ERROR:
				if l < syscall.NLMSG_HDRLEN+4 {
					return errors.New("hostbridge: bad netlink reply")
				}
				if e := int32(binary.NativeEndian.Uint32(b[syscall.NLMSG_HDRLEN:])); e != 0 { //nolint:gosec // a negative errno
					return syscall.Errno(-e) //nolint:gosec // positive here
				}
				return nil
			default:
				if each != nil {
					each(t, b[syscall.NLMSG_HDRLEN:l])
				}
			}
			b = b[min(align4(l), len(b)):]
		}
	}
}
