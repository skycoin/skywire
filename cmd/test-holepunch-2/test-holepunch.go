package main

import (
	"flag"
	"net"
	"os"
	"sync"
	"time"

	"github.com/libp2p/go-reuseport"
	"github.com/sirupsen/logrus"
)

func main() {
	flag.Parse()

	network := os.Args[1]
	src := os.Args[2]
	dst := os.Args[3]
	from := os.Args[4]
	to := os.Args[5]

	logrus.Infof("Dialing %v from %v via %v, from %v to %v", dst, src, network, from, to)

	addr, err := net.ResolveTCPAddr("tcp4", src)
	if err != nil {
		panic(err)
	}

	conn, err := reuseport.Dial(network, addr.String(), dst)
	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		buf := make([]byte, 1024)
		if n, err := conn.Read(buf); err != nil {
			logrus.Errorf("[1] Read failure: %v", err)
		} else {
			logrus.Infof("[1] Read success (%v bytes): %v", n, string(buf))
			go func() {
				if err := newConn(addr.String(), string(buf)); err != nil {
					logrus.Errorf("newConn failed: %v", err)
				}
			}()
		}
	}()

	if n, err := conn.Write([]byte(text)); err != nil {
		logrus.Errorf("[1] Write failure: %v", err)
	} else {
		logrus.Infof("[1] Write success (%v bytes)", n)
	}

	wg.Wait()

	time.Sleep(time.Minute)
}

func newConn(src, dst string) error {
	i := 0
	for {
		i++
		conn, err := reuseport.Dial("tcp", src, dst)
		if err != nil {
			logrus.Error(err)
			continue
		}

		go func() {
			buf := make([]byte, 1024)
			if n, err := conn.Read(buf); err != nil {
				logrus.Errorf("[2] Read failure: %v", err)
			} else {
				logrus.Infof("[2] Read success (%v bytes): %v, attempt %v", n, string(buf), i)
			}
		}()

		if n, err := conn.Write([]byte("ping")); err != nil {
			logrus.Errorf("[2] Write failure: %v", err)
			return err
		} else {
			logrus.Infof("[2] Write success (%v bytes) attempt %v", n, i)
		}

		return nil
	}
}
