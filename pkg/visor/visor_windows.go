//+build windows

package visor

import (
	"context"
	"errors"
)

type pty struct {
}

func (visor *Visor) startDmsgPty(ctx context.Context) error {
	return nil
}

func (visor *Visor) setupDmsgPTY() error {
	if visor.conf.DmsgPty != nil {
		return errors.New("dmsgpty is not supported for this OS")
	}

	return nil
}
