// Package skysocks internal/skysocks/common.go
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
			log.Errorf("%s IPC received error: %v", appName, err)
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
