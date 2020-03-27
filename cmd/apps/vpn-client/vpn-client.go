package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/songgao/water"
)

func run(args ...string) {
	//cmd := exec.Command("sh -c \"ip " + strings.Join(args, " ") + "\"")
	cmd := exec.Command("/usr/local/bin/ip", args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if nil != err {
		log.Fatalln("Error running /sbin/ip:", err)
	}
}

const (
	localSubnet = "10.0.0.1"
)

func main() {
	ifc, err := water.New(water.Config{
		DeviceType:             water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{},
	})
	if nil != err {
		log.Fatalln("Error allocating TUN interface:", err)
	}

	fmt.Printf("Allocated TUN %s\n", ifc.Name())

	//run("addr", "add", localSubnet, "dev", ifc.Name())
	//run("link", "set", "dev", ifc.Name(), "up")

	time.Sleep(10 * time.Minute)

	log.Fatalln("DONE")
}
