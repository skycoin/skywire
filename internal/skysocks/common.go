// Package skysocks internal/skysocks/common.go
package skysocks

import (
	"fmt"
	"time"

	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire/pkg/skyenv"
)

func listenIPC(ipcClient *ipc.Client, appName string, onClose func()) {
	time.Sleep(5 * time.Second)
	if ipcClient == nil {
		print(fmt.Sprintln("Unable to create IPC Client: server is non-existent"))
		return
	}
	for {
		m, err := ipcClient.Read()
		if err != nil {
			print(fmt.Sprintf("%s IPC received error: %v\n", appName, err))
		}

		if m != nil {
			if m.MsgType == skyenv.IPCShutdownMessageType {
				fmt.Println("Stopping " + appName + " via IPC")
				break
			}
		}

	}
	onClose()
}
