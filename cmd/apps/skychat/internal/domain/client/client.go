// Package client contains client related code for domain
package client

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"reflect"
	"runtime"

	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Client defines a chat client
type Client struct {
	appCl    *app.Client                // Skywire app client
	ipcCl    *ipc.Client                // IPC client
	netType  appnet.Type                // app netType
	port     routing.Port               // app port
	log      *logging.Logger            // app logger
	conns    map[cipher.PubKey]net.Conn // active connections
	clientCh chan string                // client channel
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

// GetConns returns a map of active connections
func (c *Client) GetConns() map[cipher.PubKey]net.Conn {
	return c.conns
}

// GetConnByPK returns the conn of the given visor pk or an error if there is no open connection to the requested visor
func (c *Client) GetConnByPK(pk cipher.PubKey) (net.Conn, error) {
	//check if conn already added
	if conn, ok := c.conns[pk]; ok {
		return conn, nil
	}
	return nil, fmt.Errorf("no conn available with the requested visor")
}

// AddConn adds the given net.Conn to the map to keep track of active connections
func (c *Client) AddConn(pk cipher.PubKey, conn net.Conn) error {
	//check if conn already added
	if _, ok := c.conns[pk]; ok {
		return fmt.Errorf("conn already added")
	}
	c.conns[pk] = conn
	return nil
}

// DeleteConn removes the given net.Conn from the map
func (c *Client) DeleteConn(pk cipher.PubKey) error {
	//check if conn is added
	if _, ok := c.conns[pk]; ok {
		delete(c.conns, pk)
		return nil
	}
	return fmt.Errorf("pk has no connection") //? handle as error?
}

// GetLog returns *logging.Logger
func (c *Client) GetLog() *logging.Logger {
	return c.log
}

// GetChannel returns chan string
func (c *Client) GetChannel() chan string {
	return c.clientCh
}

// NewClient returns *Client
func NewClient() *Client {
	c := Client{}
	c.appCl = app.NewClient(nil)
	//defer c.appCl.Close()

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		fmt.Printf("Failed to output build info: %v", err)
	}

	c.log = logging.MustGetLogger("chat")
	c.conns = make(map[cipher.PubKey]net.Conn)
	c.netType = appnet.TypeSkynet
	c.port = routing.Port(1)

	c.clientCh = make(chan string)
	//defer close(c.clientCh)

	if c.appCl != nil {
		c.SetAppStatus(appserver.AppDetailedStatusRunning)
	}

	if runtime.GOOS == "windows" {
		var err error
		c.ipcCl, err = ipc.StartClient(visorconfig.SkychatName, nil)
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

// IsEmpty checks if the client is empty
func (c *Client) IsEmpty() bool {
	return reflect.DeepEqual(*c, Client{})
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

// SetAppPort sets the appPort
func (c *Client) SetAppPort(appCl *app.Client, port routing.Port) {
	if err := appCl.SetAppPort(port); err != nil {
		print(fmt.Sprintf("Failed to set port %v: %v\n", port, err))
	}
}

// handleIPCSignal handles the ipc signal
func handleIPCSignal(client *ipc.Client) {
	for {
		m, err := client.Read()
		if err != nil {
			fmt.Printf("%s IPC received error: %v", visorconfig.SkychatName, err)
		}
		if m.MsgType == visorconfig.IPCShutdownMessageType {
			fmt.Println("Stopping " + visorconfig.SkychatName + " via IPC")
			break
		}
	}
	os.Exit(0)
}
