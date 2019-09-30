package visor

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type osExecuter struct {
	processes []*os.Process
	mu        sync.Mutex
}

func newOSExecuter() *osExecuter {
	return &osExecuter{processes: make([]*os.Process, 0)}
}

func (exc *osExecuter) Start(cmd *exec.Cmd) (int, error) {
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	exc.mu.Lock()
	exc.processes = append(exc.processes, cmd.Process)
	exc.mu.Unlock()

	return cmd.Process.Pid, nil
}

func (exc *osExecuter) Stop(pid int) (err error) {
	exc.mu.Lock()
	defer exc.mu.Unlock()

	for _, process := range exc.processes {
		if process.Pid != pid {
			continue
		}

		if sigErr := process.Signal(syscall.SIGKILL); sigErr != nil && err == nil {
			err = sigErr
		}
	}

	return err
}

func (exc *osExecuter) Wait(cmd *exec.Cmd) error {
	return cmd.Wait()
}
