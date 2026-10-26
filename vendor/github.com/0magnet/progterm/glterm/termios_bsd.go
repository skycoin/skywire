//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package glterm

import "golang.org/x/sys/unix"

const ioctlGetTermios = unix.TIOCGETA
