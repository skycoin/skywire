//+build !windows

package visor

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/SkycoinProject/dmsg/dmsgpty"
	"github.com/SkycoinProject/skycoin/src/util/logging"
)

type pty struct {
	pty *dmsgpty.Host
}

func (visor *Visor) setupDmsgPTY() error {
	if visor.conf.DmsgPty != nil {
		pty, err := visor.conf.DmsgPtyHost(visor.n.Dmsg())
		if err != nil {
			return fmt.Errorf("failed to setup pty: %v", err)
		}
		visor.pty.pty = pty
	} else {
		visor.Logger.Info("'dmsgpty' is not configured, skipping...")
	}

	return nil
}

func (visor *Visor) startDmsgPty(ctx context.Context) error {
	if visor.pty.pty == nil {
		return nil
	}

	log := visor.Logger.PackageLogger("dmsgpty")

	if err := visor.serveDmsgPtyCLI(ctx, log); err != nil {
		return err
	}

	go visor.serveDmsgPty(ctx, log)

	return nil
}

func (visor *Visor) serveDmsgPtyCLI(ctx context.Context, log *logging.Logger) error {
	if visor.conf.DmsgPty.CLINet == "unix" {
		if err := os.MkdirAll(filepath.Dir(visor.conf.DmsgPty.CLIAddr), ownerRWX); err != nil {
			log.WithError(err).Debug("Failed to prepare unix file dir.")
		}
	}

	ptyL, err := net.Listen(visor.conf.DmsgPty.CLINet, visor.conf.DmsgPty.CLIAddr)
	if err != nil {
		return fmt.Errorf("failed to start dmsgpty cli listener: %v", err)
	}

	go func() {
		log.WithField("net", visor.conf.DmsgPty.CLINet).
			WithField("addr", visor.conf.DmsgPty.CLIAddr).
			Info("Serving dmsgpty CLI.")

		if err := visor.pty.pty.ServeCLI(ctx, ptyL); err != nil {
			log.WithError(err).
				WithField("entity", "dmsgpty-host").
				WithField("func", ".ServeCLI()").
				Error()

			visor.cancel()
		}
	}()

	return nil
}

func (visor *Visor) serveDmsgPty(ctx context.Context, log *logging.Logger) {
	log.WithField("dmsg_port", visor.conf.DmsgPty.Port).
		Info("Serving dmsg.")

	if err := visor.pty.pty.ListenAndServe(ctx, visor.conf.DmsgPty.Port); err != nil {
		log.WithError(err).
			WithField("entity", "dmsgpty-host").
			WithField("func", ".ListenAndServe()").
			Error()

		visor.cancel()
	}
}
