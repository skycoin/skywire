package main

import (
	"flag"
	"net"
	"os"
	"sync"

	"github.com/sirupsen/logrus"
)

func main() {
	flag.Parse()

	network := os.Args[1]
	src := os.Args[2]
	dst := os.Args[3]

	logrus.Infof("Dialing %v from %v via %v", dst, src, network)

	addr, err := net.ResolveTCPAddr("tcp4", src)
	if err != nil {
		panic(err)
	}

	dialer := net.Dialer{
		LocalAddr: addr,
	}

	conn, err := dialer.Dial(network, dst)
	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		buf := make([]byte, 1024)
		if n, err := conn.Read(buf); err != nil {
			logrus.Error("read failure: %v", err)
		} else {
			logrus.Infof("Read success (%v bytes): %v", n, string(buf))
		}
	}()

	if n, err := conn.Write([]byte("ping")); err != nil {
		logrus.Error("write failure: %v", err)
	} else {
		logrus.Infof("Write success (%v bytes)", n)
	}

	wg.Wait()
}
