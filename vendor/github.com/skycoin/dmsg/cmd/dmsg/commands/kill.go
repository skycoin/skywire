// Package commands cmd/.../commands/kill.go
package commands

import (
	"os"
	"os/signal"
	"syscall"
)

func init() {
	//the application must stop on ctrl+c
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		sigCount := 0
		for range c {
			sigCount++
			if sigCount >= 3 {
				os.Exit(1)
			}
		}
	}()
}
