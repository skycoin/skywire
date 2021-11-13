package skysocks

import (
	"fmt"
	ipc "github.com/james-barrow/golang-ipc"
	"github.com/skycoin/skywire/pkg/skyenv"
)

func listenIPC(ipcClient *ipc.Client, appName string, onClose func()) {
	for {
		m, err := ipcClient.Read()
		if err != nil {
			fmt.Printf("%s IPC received error: %v", appName, err)
		}
		if m.MsgType == skyenv.IPCShutdownMessageType {
			fmt.Println("Stopping " + appName + " via IPC")
			break
		}
	}
	onClose()
}
