// Package commands cmd/dmsg/commands/kill.go
package commands

import (
	"os"
	"os/signal"
	"syscall"
)

func init() {
	// TEMPORARY WORKAROUND: Force exit on Ctrl+C after 3 attempts
	// This can be removed once the proper signal handling fixes are verified:
	// - dmsgC.Serve() now uses signal-aware context (not context.Background())
	// - Accept loops now check for context cancellation
	// - HTTP servers now shutdown gracefully
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
