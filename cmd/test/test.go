package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
	"unsafe"

	"github.com/sirupsen/logrus"
)

func main() {
	setChildPgidToParentPgidOrPgid()
}

func setChildPgidToTtyPgid() {
	pid := os.Getpid()
	ppid := os.Getppid()

	pgrp, err := syscall.Getpgid(pid)
	if err != nil {
		panic(err)
	}

	ppgrp, err := syscall.Getpgid(ppid)
	if err != nil {
		panic(err)
	}
	fmt.Printf("NEW process with pid %v, ppid %v, pgrp %v, ppgrp %v\n", pid, ppid, pgrp, ppgrp)

	signal.Ignore(syscall.SIGTTIN, syscall.SIGTTOU)

	if os.Getppid() != 0 {
		go func() {
			time.Sleep(5 * time.Second)

			cmd := exec.Command(os.Args[0], os.Args[1:]...)

			cmd.Stdout = os.Stdout
			cmd.Stdin = os.Stdin
			cmd.Stderr = os.Stderr
			cmd.Env = os.Environ()

			cmd.SysProcAttr = &syscall.SysProcAttr{}
			// cmd.SysProcAttr.Setctty = true
			// cmd.SysProcAttr.Setsid = true
			cmd.SysProcAttr.Foreground = true

			// tty := os.Stdout
			tty, err := os.OpenFile("/dev/ttys000", os.O_RDWR, 0)
			if err != nil {
				panic(err)
			}

			fpgrp := 0

			errno := Ioctl(tty.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&fpgrp)))
			if errno != 0 {
				panic(fmt.Sprintf("TIOCGPGRP failed with error code: %s", errno))
			}

			if fpgrp == 0 {
				panic("Foreground process group is zero")
			}

			cmd.SysProcAttr.Setpgid = true
			cmd.SysProcAttr.Pgid = fpgrp // TODO: use

			// tty = os.Stdout
			fd := tty.Fd()
			// fmt.Println("FD: ", fd)

			fmt.Printf("tty fd = %v, tty pgrp = %v\n", fd, fpgrp)

			// nfd, err := syscall.Dup(int(fd))
			// fmt.Println("new FD: ", nfd)

			cmd.SysProcAttr.Ctty = int(fd)

			ppid, ppgrp := parent()

			if err := cmd.Start(); err != nil {
				panic(err)
			}

			cpid, cpgrp := Info(cmd)

			fmt.Printf("just started process with pid = %v, pgrp = %v\n", cpid, cpgrp)

			if cpid == ppid {
				// panic("Parent and child have the same process ID")
			}

			if cpgrp == ppgrp {
				// panic("Parent and child are in the same process group")
			}

			if cpid != cpgrp {
				// panic("Child's process group is not the child's process ID")
			}

			fmt.Printf("setting tty pgrp to %v\n", fpgrp)

			errno = Ioctl(tty.Fd(), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&fpgrp)))
			if errno != 0 {
				panic(fmt.Sprintf("TIOCSPGRP failed with error code: %s", errno))
			}

			signal.Reset()

			time.Sleep(1 * time.Second)
			os.Exit(0)
		}()
	}

	i := 0
	for {
		logrus.Info(i)
		i++
		time.Sleep(1 * time.Second)
	}
}

func setChildPgidToParentPgidOrPgid() {
	pid := os.Getpid()
	ppid := os.Getppid()

	pgrp, err := syscall.Getpgid(pid)
	if err != nil {
		panic(err)
	}

	ppgrp, err := syscall.Getpgid(ppid)
	if err != nil {
		panic(err)
	}
	fmt.Printf("NEW process with pid %v, ppid %v, pgrp %v, ppgrp %v\n", pid, ppid, pgrp, ppgrp)

	signal.Ignore(syscall.SIGTTIN, syscall.SIGTTOU)

	if os.Getppid() != 0 {
		go func() {
			time.Sleep(5 * time.Second)

			cmd := exec.Command(os.Args[0], os.Args[1:]...)

			cmd.Stdout = os.Stdout
			cmd.Stdin = os.Stdin
			cmd.Stderr = os.Stderr
			cmd.Env = os.Environ()

			cmd.SysProcAttr = &syscall.SysProcAttr{}
			// cmd.SysProcAttr.Setctty = true
			// cmd.SysProcAttr.Setsid = true
			cmd.SysProcAttr.Foreground = true

			// tty := os.Stdout
			tty, err := os.OpenFile("/dev/ttys000", os.O_RDWR, 0)
			if err != nil {
				panic(err)
			}

			fpgrp := 0

			errno := Ioctl(tty.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&fpgrp)))
			if errno != 0 {
				panic(fmt.Sprintf("TIOCGPGRP failed with error code: %s", errno))
			}

			if fpgrp == 0 {
				panic("Foreground process group is zero")
			}

			cmd.SysProcAttr.Setpgid = true

			if n, err := syscall.Getpgid(ppgrp); err == nil && n >= 0 {
				fmt.Printf("seting parent pgrp (%v) as pgid for new proc\n", ppgrp)
				cmd.SysProcAttr.Pgid = ppgrp
			} else if n, err := syscall.Getpgid(pgrp); err == nil && n >= 0 {
				fmt.Printf("seting pgrp (%v) as pgid for new proc\n", pgrp)
				cmd.SysProcAttr.Pgid = pgrp
			}

			// tty = os.Stdout
			fd := tty.Fd()
			// fmt.Println("FD: ", fd)

			fmt.Printf("tty fd = %v, tty pgrp = %v\n", fd, fpgrp)

			// nfd, err := syscall.Dup(int(fd))
			// fmt.Println("new FD: ", nfd)

			cmd.SysProcAttr.Ctty = int(fd)

			ppid, ppgrp := parent()

			if err := cmd.Start(); err != nil {
				panic(err)
			}

			cpid, cpgrp := Info(cmd)

			fmt.Printf("just started process with pid = %v, pgrp = %v\n", cpid, cpgrp)

			if cpid == ppid {
				// panic("Parent and child have the same process ID")
			}

			if cpgrp == ppgrp {
				// panic("Parent and child are in the same process group")
			}

			if cpid != cpgrp {
				// panic("Child's process group is not the child's process ID")
			}

			// fmt.Printf("setting tty pgrp to %v\n", fpgrp)

			// errno = Ioctl(tty.Fd(), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&fpgrp)))
			// if errno != 0 {
			// 	panic(fmt.Sprintf("TIOCSPGRP failed with error code: %s", errno))
			// }

			signal.Reset()

			time.Sleep(1 * time.Second)
			os.Exit(0)
		}()
	}

	i := 0
	for {
		logrus.Info(i)
		i++
		time.Sleep(1 * time.Second)
	}
}

func setTtyPgidToChildPgid() {
	pid := os.Getpid()
	ppid := os.Getppid()

	pgrp, err := syscall.Getpgid(pid)
	if err != nil {
		panic(err)
	}

	ppgrp, err := syscall.Getpgid(ppid)
	if err != nil {
		panic(err)
	}
	fmt.Printf("NEW process with pid %v, ppid %v, pgrp %v, ppgrp %v\n", pid, ppid, pgrp, ppgrp)

	signal.Ignore(syscall.SIGTTIN, syscall.SIGTTOU)

	if os.Getppid() != 0 {
		go func() {
			time.Sleep(5 * time.Second)

			cmd := exec.Command(os.Args[0], os.Args[1:]...)

			cmd.Stdout = os.Stdout
			cmd.Stdin = os.Stdin
			cmd.Stderr = os.Stderr
			cmd.Env = os.Environ()

			cmd.SysProcAttr = &syscall.SysProcAttr{}
			// cmd.SysProcAttr.Setctty = true
			// cmd.SysProcAttr.Setsid = true
			cmd.SysProcAttr.Foreground = true

			// tty := os.Stdout
			tty, err := os.OpenFile("/dev/ttys000", os.O_RDWR, 0)
			if err != nil {
				panic(err)
			}

			fpgrp := 0

			errno := Ioctl(tty.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&fpgrp)))
			if errno != 0 {
				panic(fmt.Sprintf("TIOCGPGRP failed with error code: %s", errno))
			}

			if fpgrp == 0 {
				panic("Foreground process group is zero")
			}

			// cmd.SysProcAttr.Setpgid = true
			// cmd.SysProcAttr.Pgid = fpgrp // TODO: use

			// tty = os.Stdout
			fd := tty.Fd()
			// fmt.Println("FD: ", fd)

			fmt.Printf("tty fd = %v, tty pgrp = %v\n", fd, fpgrp)

			// nfd, err := syscall.Dup(int(fd))
			// fmt.Println("new FD: ", nfd)

			cmd.SysProcAttr.Ctty = int(fd)

			ppid, ppgrp := parent()

			if err := cmd.Start(); err != nil {
				panic(err)
			}

			cpid, cpgrp := Info(cmd)

			fmt.Printf("just started process with pid = %v, pgrp = %v\n", cpid, cpgrp)

			if cpid == ppid {
				// panic("Parent and child have the same process ID")
			}

			if cpgrp == ppgrp {
				// panic("Parent and child are in the same process group")
			}

			if cpid != cpgrp {
				// panic("Child's process group is not the child's process ID")
			}

			fmt.Printf("setting tty pgrp to %v\n", cpgrp)

			errno = Ioctl(tty.Fd(), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&cpgrp)))
			if errno != 0 {
				panic(fmt.Sprintf("TIOCSPGRP failed with error code: %s", errno))
			}

			signal.Reset()

			time.Sleep(1 * time.Second)
			os.Exit(0)
		}()
	}

	i := 0
	for {
		logrus.Info(i)
		i++
		time.Sleep(1 * time.Second)
	}
}

func setChildPgidToPgid() {
	pid := os.Getpid()
	ppid := os.Getppid()

	pgrp, err := syscall.Getpgid(pid)
	if err != nil {
		panic(err)
	}

	ppgrp, err := syscall.Getpgid(ppid)
	if err != nil {
		panic(err)
	}

	fmt.Printf("NEW process with pid %v, ppid %v, pgrp %v, ppgrp %v\n", pid, ppid, pgrp, ppgrp)

	// if err := syscall.Setpgid(pid, ppgrp); err != nil {
	// 	panic(err)
	// }
	//
	// fmt.Printf("Set pgid %v for self (%v)\n", ppgrp, pid)

	signal.Ignore(syscall.SIGTTIN, syscall.SIGTTOU)

	if os.Getppid() != 0 {
		go func() {
			time.Sleep(5 * time.Second)

			cmd := exec.Command(os.Args[0], os.Args[1:]...)

			cmd.Stdout = os.Stdout
			cmd.Stdin = os.Stdin
			cmd.Stderr = os.Stderr
			cmd.Env = os.Environ()

			cmd.SysProcAttr = &syscall.SysProcAttr{}
			// cmd.SysProcAttr.Setctty = true
			// cmd.SysProcAttr.Setsid = true
			cmd.SysProcAttr.Foreground = true

			// tty := os.Stdout
			tty, err := os.OpenFile("/dev/ttys000", os.O_RDWR, 0)
			if err != nil {
				panic(err)
			}

			fpgrp := 0

			errno := Ioctl(tty.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&fpgrp)))
			if errno != 0 {
				panic(fmt.Sprintf("TIOCGPGRP failed with error code: %s", errno))
			}

			if fpgrp == 0 {
				panic("Foreground process group is zero")
			}

			cmd.SysProcAttr.Setpgid = true
			cmd.SysProcAttr.Pgid = pgrp // TODO: use

			// tty = os.Stdout
			fd := tty.Fd()
			// fmt.Println("FD: ", fd)

			fmt.Printf("tty fd = %v, tty pgrp = %v\n", fd, fpgrp)

			// nfd, err := syscall.Dup(int(fd))
			// fmt.Println("new FD: ", nfd)

			cmd.SysProcAttr.Ctty = int(fd)

			ppid, ppgrp := parent()

			if err := cmd.Start(); err != nil {
				panic(err)
			}

			cpid, cpgrp := Info(cmd)

			fmt.Printf("just started process with pid = %v, pgrp = %v\n", cpid, cpgrp)

			if cpid == ppid {
				// panic("Parent and child have the same process ID")
			}

			if cpgrp == ppgrp {
				// panic("Parent and child are in the same process group")
			}

			if cpid != cpgrp {
				// panic("Child's process group is not the child's process ID")
			}

			// fmt.Printf("setting tty pgrp to %v\n", fpgrp)

			// errno = Ioctl(tty.Fd(), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&fpgrp)))
			// if errno != 0 {
			// 	panic(fmt.Sprintf("TIOCSPGRP failed with error code: %s", errno))
			// }

			// signal.Reset()

			time.Sleep(1 * time.Second)
			os.Exit(0)
		}()
	}

	i := 0
	for {
		logrus.Info(i)
		i++
		time.Sleep(1 * time.Second)
	}
}

func Ioctl(fd, req, arg uintptr) (err syscall.Errno) {
	_, _, err = syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg)
	return err
}

func parent() (pid, pgrp int) {
	return syscall.Getpid(), syscall.Getpgrp()
}

func Info(c *exec.Cmd) (pid, pgrp int) {
	pid = c.Process.Pid

	pgrp, err := syscall.Getpgid(pid)
	if err != nil {
		panic(err)
	}

	return
}
