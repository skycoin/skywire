package app2

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
)

type Proc struct {
	id  ProcID
	cmd *exec.Cmd
	mx  sync.RWMutex
}

func NewProc(c Config, args []string, key Key) *Proc {
	binaryPath := getBinaryPath(c.BinaryDir, c.Name, c.Version)

	const (
		appKeyEnvFormat   = "APP_KEY=%s"
		sockFileEnvFormat = "SW_UNIX=%s"
	)

	env := make([]string, 0, 2)
	env = append(env, fmt.Sprintf(appKeyEnvFormat, key))
	env = append(env, fmt.Sprintf(sockFileEnvFormat, c.SockFile))

	cmd := exec.Command(binaryPath, args...) // nolint:gosec

	cmd.Env = env
	cmd.Dir = c.WorkDir

	return &Proc{
		cmd: cmd,
	}
}

func (p *Proc) ID() ProcID {
	p.mx.RLock()
	id := p.id
	p.mx.RUnlock()
	return id
}

func (p *Proc) Run() error {
	if err := p.cmd.Run(); err != nil {
		return err
	}

	p.mx.Lock()
	p.id = ProcID(p.cmd.Process.Pid)
	p.mx.Unlock()

	return nil
}

func (p *Proc) Stop() error {
	return p.cmd.Process.Kill()
}

func (p *Proc) Wait() error {
	return p.cmd.Wait()
}

func getBinaryPath(dir, name, ver string) string {
	const binaryNameFormat = "%s.v%s"
	return filepath.Join(dir, fmt.Sprintf(binaryNameFormat, name, ver))
}
