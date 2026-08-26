// Package skysocks pkg/skysocks/common.go c4-app-proxy
package skysocks

import (
	"time"

	ipc "github.com/james-barrow/golang-ipc"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/skyenv"
)

func listenIPC(ipcClient *ipc.Client, appName string, log logrus.FieldLogger, onClose func()) {
	time.Sleep(5 * time.Second)
	if ipcClient == nil {
		log.Error("Unable to create IPC Client: server is non-existent")
		return
	}
	for {
		m, err := ipcClient.Read()
		if err != nil {
			// A read error is terminal: golang-ipc closes the receive channel
			// on error, so every subsequent Read returns immediately with the
			// same error. Without breaking, this loop spins at full tilt
			// (hundreds of log lines per millisecond), pegging a CPU core and
			// starving the app's real work — the same regression already fixed
			// for skychat's IPC signal loop. Stop the handler and run onClose so
			// the app tears down cleanly instead of busy-looping.
			log.Errorf("%s IPC read error, stopping IPC handler: %v", appName, err)
			break
		}

		if m != nil {
			if m.MsgType == skyenv.IPCShutdownMessageType {
				log.Infof("Stopping %s via IPC", appName)
				break
			}
		}

	}
	onClose()
}
