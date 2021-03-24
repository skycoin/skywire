//+build !windows

package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/skycoin/dmsg/dmsgpty"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire/pkg/util/osutil"
)

const ownerRWX = 0700

func initDmsgpty(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	report := v.makeReporter("dmsgpty")
	conf := v.conf.Dmsgpty

	if conf == nil {
		log.Info("'dmsgpty' is not configured, skipping.")
		return nil
	}

	// Unlink dmsg socket files (just in case).
	if conf.CLINet == "unix" {
		if err := osutil.UnlinkSocketFiles(v.conf.Dmsgpty.CLIAddr); err != nil {
			report(err)
			return err
		}
	}

	var wl dmsgpty.Whitelist
	if conf.AuthFile == "" {
		wl = dmsgpty.NewMemoryWhitelist()
	} else {
		var err error
		if wl, err = dmsgpty.NewJSONFileWhiteList(v.conf.Dmsgpty.AuthFile); err != nil {
			report(err)
			return err
		}
	}

	// Ensure hypervisors are added to the whitelist.
	if err := wl.Add(v.conf.Hypervisors...); err != nil {
		report(err)
		return err
	}
	// add itself to the whitelist to allow local pty
	if err := wl.Add(v.conf.PK); err != nil {
		v.log.Errorf("Cannot add itself to the pty whitelist: %s", err)
	}

	dmsgC := v.net.Dmsg()
	if dmsgC == nil {
		err := errors.New("cannot create dmsgpty with nil dmsg client")
		report(err)
		return err
	}

	pty := dmsgpty.NewHost(dmsgC, wl)

	if ptyPort := conf.Port; ptyPort != 0 {
		ctx, cancel := context.WithCancel(context.Background())
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func() {
			defer wg.Done()
			if err := pty.ListenAndServe(ctx, ptyPort); err != nil {
				report(fmt.Errorf("listen and serve stopped: %w", err))
			}
		}()

		v.pushCloseStack("dmsgpty.serve", func() bool {
			cancel()
			wg.Wait()
			return report(nil)
		})
	}

	if conf.CLINet != "" {
		if conf.CLINet == "unix" {
			if err := os.MkdirAll(filepath.Dir(conf.CLIAddr), ownerRWX); err != nil {
				err := fmt.Errorf("failed to prepare unix file for dmsgpty cli listener: %w", err)
				report(err)
				return err
			}
		}

		cliL, err := net.Listen(conf.CLINet, conf.CLIAddr)
		if err != nil {
			err := fmt.Errorf("failed to start dmsgpty cli listener: %w", err)
			report(err)
			return err
		}

		ctx, cancel := context.WithCancel(context.Background())
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func() {
			defer wg.Done()
			if err := pty.ServeCLI(ctx, cliL); err != nil {
				report(fmt.Errorf("serve cli stopped: %w", err))
			}
		}()

		v.pushCloseStack("dmsgpty.cli", func() bool {
			cancel()
			ok := report(cliL.Close())
			wg.Wait()
			return ok
		})
	}

	return nil
}
