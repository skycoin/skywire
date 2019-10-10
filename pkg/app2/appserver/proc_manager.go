package appserver

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type ProcManager struct {
	processes []*os.Process
	mu        sync.Mutex
}

func NewProcManager() *ProcManager {
	return &ProcManager{processes: make([]*os.Process, 0)}
}

func (m *ProcManager) Start(cmd *exec.Cmd) (int, error) {
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	m.mu.Lock()
	m.processes = append(m.processes, cmd.Process)
	m.mu.Unlock()

	return cmd.Process.Pid, nil
}

func (m *ProcManager) Stop(pid int) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, process := range m.processes {
		if process.Pid != pid {
			continue
		}

		if sigErr := process.Signal(syscall.SIGKILL); sigErr != nil && err == nil {
			err = sigErr
		}
	}

	return err
}

func (m *ProcManager) Wait(cmd *exec.Cmd) error {
	return cmd.Wait()
}
