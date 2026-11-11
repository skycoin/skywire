//go:build linux

// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package splice

import (
	"fmt"
	"log"
	"os"
	"syscall"
)

type Pair struct {
	r, w         *os.File
	rConn, wConn syscall.RawConn
	size         int
}

func (p *Pair) MaxGrow() {
	for p.Grow(2*p.size) == nil {
	}
}

func (p *Pair) Grow(n int) error {
	if n <= p.size {
		return nil
	}
	if !resizable {
		return fmt.Errorf("splice: want %d bytes, but not resizable", n)
	}
	if n > maxPipeSize {
		return fmt.Errorf("splice: want %d bytes, max pipe size %d", n, maxPipeSize)
	}

	var newsize int
	var errNo syscall.Errno
	p.rConn.Control(func(fd uintptr) {
		newsize, errNo = fcntl(fd, F_SETPIPE_SZ, n)
	})
	if errNo != 0 {
		return fmt.Errorf("splice: fcntl returned %v", errNo)
	}
	p.size = newsize
	return nil
}

func (p *Pair) Cap() int {
	return p.size
}

func (p *Pair) Close() error {
	if p.r == nil {
		log.Panicf("2nd close r")
	}
	err1 := p.r.Close()
	p.r = nil
	if p.w == nil {
		log.Panicf("2nd close w")
	}
	err2 := p.w.Close()
	p.w = nil
	if err1 != nil {
		return err1
	}
	return err2
}

func (p *Pair) Read(d []byte) (n int, err error) {
	p.rConn.Control(func(fd uintptr) {
		n, err = syscall.Read(int(fd), d)
	})
	return
}

func (p *Pair) Write(d []byte) (n int, err error) {
	p.wConn.Control(func(fd uintptr) {
		n, err = syscall.Write(int(fd), d)
	})
	return
}

func (p *Pair) ReadFd() uintptr {
	var fd uintptr
	p.rConn.Control(func(f uintptr) { fd = f })
	return fd
}

func (p *Pair) WriteFd() uintptr {
	var fd uintptr
	p.wConn.Control(func(f uintptr) { fd = f })
	return fd
}
