//+build windows

package visor

import (
	"context"
	"errors"
)

type pty struct {
}

func (visor *Visor) startDmsgPty(ctx context.Context) error {
	return errors.New("dmsgpty is not supported for this OS")
}

func (visor *Visor) setupDmsgPTY() error {
	return errors.New("dmsgpty is not supported for this OS")
}
