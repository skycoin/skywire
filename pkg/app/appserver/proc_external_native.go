//go:build !tinygo && !js

// Package appserver pkg/app/appserver/proc_external_native.go
//
// External (OS-subprocess) launch path for a Proc. It uses os/exec and the
// james-barrow/golang-ipc Windows IPC server, neither of which compiles on the
// TinyGo js/wasm target. proc.go therefore types Proc.cmd / Proc.ipcServer as
// `any` and routes every os/exec- and ipc-touching operation through the
// helpers below; proc_external_tinygo.go provides fail-closed stubs so an
// edge-only browser visor (which runs apps in-process, RunModeInternal) still
// compiles. See appcommon.RunModeExternal vs RunModeInternal.
package appserver

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"

	ipc "github.com/james-barrow/golang-ipc"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// newExternalCmd builds the *exec.Cmd for an external-mode app (nil for
// in-process apps). Returned as `any` to match the Proc.cmd field type.
func newExternalCmd(conf appcommon.ProcConfig, mLog *logging.MasterLogger, moduleName string) any {
	if conf.EffectiveRunMode() != appcommon.RunModeExternal {
		return nil
	}
	envs := conf.Envs()
	cmd := exec.Command(conf.BinaryLoc, conf.ProcArgs...) //nolint:gosec
	cmd.Env = append(os.Environ(), envs...)
	cmd.Dir = conf.ProcWorkDir
	// Drop privileges before exec when AppConfig set User/Group.
	// applyProcCredentials is POSIX-only (the windows build returns
	// a hard error so a misconfiguration surfaces loudly). When
	// the lookup fails or the visor lacks CAP_SETUID, we leave the
	// cmd ready-to-run and let exec.Start surface the kernel's
	// EPERM at startup; the resulting log line points at proc.go.
	if conf.ProcUser != "" {
		if cerr := applyProcCredentials(cmd, conf.ProcUser, conf.ProcGroup); cerr != nil {
			mLog.PackageLogger(moduleName).WithError(cerr).
				Warnf("Could not set credentials for %s; spawning under the visor's UID instead", conf.AppName)
		}
	}
	return cmd
}

// attachCmdLogs wires an external cmd's stdout/stderr to the app logger and
// returns the stderr pipe (nil for in-process apps).
func attachCmdLogs(cmdAny any, appLog *logging.MasterLogger, moduleName string) io.ReadCloser {
	cmd, _ := cmdAny.(*exec.Cmd)
	if cmd == nil {
		return nil
	}
	cmd.Stdout = appLog.WithField("_module", moduleName).WithField("func", "(STDOUT)").WriterLevel(logrus.DebugLevel)

	errorLog := appLog.WithField("_module", moduleName).WithField("func", "(STDERR)")
	stderr, _ := cmd.StderrPipe() //nolint:errcheck
	printStdErr(stderr, errorLog)
	return stderr
}

// Cmd returns the underlying os/exec command (nil for in-process apps).
func (p *Proc) Cmd() *exec.Cmd {
	cmd, _ := p.cmd.(*exec.Cmd)
	return cmd
}

// pid returns the external app's OS process id, or 0 for in-process apps.
func (p *Proc) pid() int {
	cmd, _ := p.cmd.(*exec.Cmd)
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

// isExitError reports whether err is an *exec.ExitError (a non-zero process
// exit), which the proc manager treats as a normal app exit rather than a
// launch failure.
func isExitError(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}

// startWindowsIPCServer starts the Windows IPC server for an app (no-op off
// Windows). Used by both the in-process and external start paths.
func (p *Proc) startWindowsIPCServer() error {
	if runtime.GOOS != "windows" {
		return nil
	}
	ipcServer, err := ipc.StartServer(p.appName, nil)
	if err != nil {
		return err
	}
	p.ipcServer = ipcServer
	p.ipcServerWg.Done()
	return nil
}

// signalStop delivers the stop signal to an external app: SIGINT on POSIX, an
// IPC shutdown message on Windows. No-op for in-process apps (nil cmd).
func (p *Proc) signalStop() error {
	cmd, _ := p.cmd.(*exec.Cmd)
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if runtime.GOOS != "windows" {
		return cmd.Process.Signal(os.Interrupt)
	}
	p.ipcServerWg.Wait()
	if srv, ok := p.ipcServer.(*ipc.Server); ok && srv != nil {
		return srv.Write(skyenv.IPCShutdownMessageType, []byte(""))
	}
	return nil
}

// closeIPCServer closes the Windows IPC server if one is running.
func (p *Proc) closeIPCServer() {
	if srv, ok := p.ipcServer.(*ipc.Server); ok && srv != nil {
		srv.Close()
	}
}

// startExternal starts the app as a separate OS process.
func (p *Proc) startExternal() error {
	cmd, _ := p.cmd.(*exec.Cmd)

	// Lifecycle context, canceled on teardown (Stop -> appCancelCtx, and the
	// deferred m.Stop in the goroutine below). Mirrors startInProcess so the
	// readyCh waiter can wake on teardown instead of leaking when an external
	// app dies before it ever reports Running.
	p.appCtx, p.appCancelCtx = context.WithCancel(context.Background()) //nolint:gosec

	if err := cmd.Start(); err != nil {
		p.waitMx.Unlock()
		return err
	}

	go func() {
		waitErrCh := make(chan error)
		go func() {
			waitErrCh <- cmd.Wait()
			close(waitErrCh)
		}()

		defer func() {
			_ = p.m.SetError(p.appName, p.err) //nolint:errcheck
			_ = p.m.Stop(p.appName)            //nolint:errcheck
		}()

		select {
		case _, ok := <-p.connCh:
			if !ok {
				_ = cmd.Process.Kill() //nolint:errcheck
				p.waitMx.Unlock()

				return
			}
		case waitErr := <-waitErrCh:
			p.waitErr = waitErr
			p.waitMx.Unlock()

			p.connOnce.Do(func() { close(p.connCh) })

			return
		}

		if ok := p.AwaitConn(); !ok {
			_ = cmd.Process.Kill() //nolint:errcheck
			p.waitMx.Unlock()
			return
		}

		go func() {
			select {
			case <-p.readyCh:
				p.disc.Start()
			case <-p.appCtx.Done():
				// Torn down before the app reported Running — don't start
				// discovery for a now-dead app; just exit instead of leaking
				// this goroutine blocked on readyCh forever.
			}
		}()
		defer p.disc.Stop()

		if runtime.GOOS == "windows" {
			ipcServer, err := ipc.StartServer(p.appName, nil)
			if err != nil {
				_ = cmd.Process.Kill() //nolint:errcheck
				p.waitMx.Unlock()
				p.ipcServerWg.Done()
				return
			}
			p.ipcServer = ipcServer
			p.ipcServerWg.Done()
		}

		p.waitErr = <-waitErrCh

		if err := p.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			p.log.WithError(err).Warn("Closing proc conn returned unexpected error.")
		}
		p.rpcGW.cm.CloseAll()
		p.rpcGW.lm.CloseAll()

		p.waitMx.Unlock()
	}()

	return nil
}
