// Package cmdutil pkg/cmdutil/signal_context.go c0-com-util
package cmdutil

import (
	"context"
	"os"
	"os/signal"

	"github.com/sirupsen/logrus"
)

// SignalContext returns a context that cancels on given syscall signals.
func SignalContext(ctx context.Context, log logrus.FieldLogger) (context.Context, context.CancelFunc) {
	if log == nil {
		l := logrus.New()
		l.SetOutput(os.Stderr)
		log = l
	}

	ctx, cancel := context.WithCancel(ctx) //nolint:gosec // cancel is called on signal receipt below

	ch := make(chan os.Signal, 1)
	listenSigs := listenSignals()
	signal.Notify(ch, listenSigs...)
	// js/wasm: nothing ever delivers a POSIX signal, so the platform hook
	// registers a JS-callable interrupt instead (the browser terminal's
	// Ctrl+C reaches a foreground visor through it). No-op on native.
	notifyPlatformInterrupt(ch)

	go func() {
		select {
		case sig := <-ch:
			log.WithField("signal", sig).
				Info("Closing with received signal.")
		case <-ctx.Done():
		}
		cancel()
	}()

	return ctx, cancel
}
