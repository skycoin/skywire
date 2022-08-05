package client

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"

	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Client defines a chat client
type Client struct {
	appCl    *app.Client     // Skywire app client
	ipcCl    *ipc.Client     // IPC client
	netType  appnet.Type     // app netType
	port     routing.Port    // app port
	log      *logging.Logger // app logger
	clientCh chan string     // client channel
}

// Getter

// GetAppClient returns *app.Client
func (c *Client) GetAppClient() *app.Client {
	return c.appCl
}

// GetNetType returns appnet.Type
func (c *Client) GetNetType() appnet.Type {
	return c.netType
}

// GetPort returns routing.Port
func (c *Client) GetPort() routing.Port {
	return c.port
}

// GetLog returns *logging.Logger
func (c *Client) GetLog() *logging.Logger {
	return c.log
}

//  GetChannel returns chan string
func (c *Client) GetChannel() chan string {
	return c.clientCh
}

// NewClient retrns *Client
func NewClient() *Client {
	c := Client{}
	c.appCl = app.NewClient(nil)
	//defer c.appCl.Close()

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		fmt.Printf("Failed to output build info: %v", err)
	}

	c.log = logging.MustGetLogger("chat")
	c.netType = appnet.TypeSkynet
	c.port = routing.Port(1)

	c.clientCh = make(chan string)
	//defer close(c.clientCh)

	if c.appCl != nil {
		c.SetAppStatus(appserver.AppDetailedStatusRunning)
	}

	if runtime.GOOS == "windows" {
		var err error
		c.ipcCl, err = ipc.StartClient(skyenv.SkychatName, nil)
		if err != nil {
			fmt.Printf("Error creating ipc server for skychat client: %v\n", err)
			c.SetAppError(err)
			os.Exit(1)
		}
		go handleIPCSignal(c.ipcCl)
	}

	if runtime.GOOS != "windows" {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)

		go func() {
			<-termCh
			c.SetAppStatus(appserver.AppDetailedStatusStopped)
			os.Exit(1)
		}()
	}

	return &c
}

// IsEmtpy checks if the cient is empty
func (c *Client) IsEmtpy() bool {
	return *c == Client{}
}

// SetAppStatus sets appserver.AppDetailedStatus
func (c *Client) SetAppStatus(status appserver.AppDetailedStatus) {
	err := c.appCl.SetDetailedStatus(string(status))
	if err != nil {
		fmt.Printf("Failed to set status %v: %v\n", status, err)
	}
}

// SetAppError sets the appErr.Error
func (c *Client) SetAppError(appErr error) {
	err := c.appCl.SetError(appErr.Error())
	if err != nil {
		fmt.Printf("Failed to set error %v: %v\n", appErr, err)
	}
}

func handleIPCSignal(client *ipc.Client) {
	for {
		m, err := client.Read()
		if err != nil {
			fmt.Printf("%s IPC received error: %v", skyenv.SkychatName, err)
		}
		if m.MsgType == skyenv.IPCShutdownMessageType {
			fmt.Println("Stopping " + skyenv.SkychatName + " via IPC")
			break
		}
	}
	os.Exit(0)
}
