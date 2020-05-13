//+build windows

package visor

import (
	"context"
	"errors"
)

// pty is a dummy serving to exclude source file with `*dmsgpty.Host` from building.
type pty struct {
}

func (visor *Visor) setupDmsgPTY() error {
	if visor.conf.DmsgPty != nil {
		// we return error only if the user stated `dmsgpty` in the config
		return errors.New("dmsgpty is not supported for this OS")
	}

	return nil
}

func (visor *Visor) startDmsgPty(ctx context.Context) error {
	// this one will be called anyway, so we just return no error to continue execution
	return nil
}
