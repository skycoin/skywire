// +build windows

package commands

import (
	"context"
	"os"
	"os/signal"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

func detachProcess(_ time.Duration, _ logrus.FieldLogger) {
}

// signalContext is just wrapper for cmdutil.SignalContext with its signal uses windows specific signals
func signalContext(ctx context.Context, log logrus.FieldLogger) (context.Context, context.CancelFunc) {
	if log == nil {
		log = logrus.New()
	}

	ctx, cancel := context.WithCancel(ctx)
	ch := make(chan os.Signal)

	signal.Notify(ch, []os.Signal{windows.SIGINT, windows.SIGTERM, windows.SIGQUIT}...)

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
