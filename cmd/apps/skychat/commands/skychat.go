// Package commands cmd/apps/skychat/skychat.go
package commands

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"time"

	ipc "github.com/james-barrow/golang-ipc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var r = netutil.NewRetrier(nil, 50*time.Millisecond, netutil.DefaultMaxBackoff, 5, 2)

var (
	addr       string
	appCl      *app.Client
	appLog     func(format string, args ...interface{}) // App logger function
	clientCh   chan string
	conns      map[cipher.PubKey]net.Conn // Chat connections
	connsMu    sync.Mutex
	appPort    uint16
	useSkynet  bool
	useDmsg    bool
)

// the go embed static points to skywire/cmd/apps/skychat/static

//go:embed static
var embededFiles embed.FS

func init() {
	launcher.RegisterApp("skychat", RunSkychat)
	RootCmd.Flags().StringVar(&addr, "addr", ":8001", "address to bind, put an * before the port if you want to be able to access outside localhost")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
	RootCmd.Flags().BoolVar(&useSkynet, "skynet", true, "listen on skynet network")
	RootCmd.Flags().BoolVar(&useDmsg, "dmsg", true, "listen on dmsg network")
}

// RootCmd is the root command for skywire-cli
var RootCmd = &cobra.Command{
	Use:                   "skychat",
	Short:                 "skywire chat application",
	Long:                  calvin.AsciiFont("skychat"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()

		if err := RunSkychat(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

// RunSkychat runs the skychat app logic. This can be called from the visor or from the CLI.
func RunSkychat(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		// Create independent FlagSet for parsing without initialization cycle
		fs := pflag.NewFlagSet("skychat", pflag.ContinueOnError)
		fs.StringVar(&addr, "addr", ":8001", "address to bind")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		fs.BoolVar(&useSkynet, "skynet", true, "listen on skynet")
		fs.BoolVar(&useDmsg, "dmsg", true, "listen on dmsg")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	// Wrap context with cancel to allow graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	appCl = app.NewClient(nil)
	defer appCl.Close()

	// Set up app logger
	appLog = func(format string, args ...interface{}) {
		appCl.Log().Infof(format, args...)
	}

	appLog("Build info: %s", buildinfo.Version())
	appLog("Successfully started skychat.")

	clientCh = make(chan string)
	defer close(clientCh)

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
		setAppPort(appCl, port)
	}

	conns = make(map[cipher.PubKey]net.Conn)

	if useSkynet {
		go listenLoop(appnet.TypeSkynet, port)
	}
	if useDmsg {
		go listenLoop(appnet.TypeDmsg, port)
	}
	if !useSkynet && !useDmsg {
		appLog("Warning: no network types enabled, skychat will not accept connections")
	}

	if runtime.GOOS == "windows" {
		ipcClient, err := ipc.StartClient(visorconfig.SkychatName, nil)
		if err != nil {
			appLog("Error creating ipc server for skychat client: %v", err)
			setAppError(appCl, err)
			return err
		}
		go handleIPCSignal(ipcClient)
	}

	http.Handle("/", http.FileServer(getFileSystem()))
	http.HandleFunc("/message", messageHandler(ctx))
	http.HandleFunc("/sse", sseHandler)

	url := ""
	address := addr
	if len(address) < 5 || (address[:1] != ":" && address[:2] != "*:") {
		url = "127.0.0.1:8001"
	} else if address[:1] == ":" {
		url = "127.0.0.1" + address
	} else if address[:2] == "*:" {
		url = "0.0.0.0" + address[1:]
	} else {
		url = "127.0.0.1:8001"
	}

	appLog("Serving HTTP on %s", url)

	if runtime.GOOS != "windows" {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)

		go func() {
			select {
			case <-termCh:
				setAppStatus(appCl, appserver.AppDetailedStatusStopped)
				cancel()
			case <-ctx.Done():
				setAppStatus(appCl, appserver.AppDetailedStatusStopped)
				return
			}
		}()
	}

	setAppStatus(appCl, appserver.AppDetailedStatusRunning)
	srv := &http.Server{
		Addr:         url,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			appLog("HTTP server error: %v", err)
			setAppError(appCl, err)
			return err
		}
	case <-ctx.Done():
		setAppStatus(appCl, appserver.AppDetailedStatusStopped)
		if err := srv.Shutdown(context.Background()); err != nil {
			return err
		}
	}

	return nil
}

func listenLoop(netType appnet.Type, appPort routing.Port) {
	l, err := appCl.Listen(netType, appPort)
	if err != nil {
		appLog("Error listening network %v on port %d: %v", netType, appPort, err)
		setAppError(appCl, err)
		return
	}

	appLog("Listening on %s network, port %d", netType, appPort)

	for {
		appCl.Log().Debugf("Accepting skychat conn on %s...", netType)
		conn, err := l.Accept()
		if err != nil {
			appLog("Failed to accept conn on %s: %v", netType, err)
			return
		}
		appCl.Log().Debugf("Accepted skychat conn on %s", netType)

		raddr := conn.RemoteAddr().(appnet.Addr)
		connsMu.Lock()
		conns[raddr.PubKey] = conn
		connsMu.Unlock()
		appLog("Accepted skychat conn on %s from %s via %s", conn.LocalAddr(), raddr.PubKey, netType)

		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	raddr := conn.RemoteAddr().(appnet.Addr)
	for {
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			appLog("Failed to read packet: %v", err)
			raddr := conn.RemoteAddr().(appnet.Addr)
			connsMu.Lock()
			delete(conns, raddr.PubKey)
			connsMu.Unlock()
			return
		}

		clientMsg, err := json.Marshal(map[string]string{"sender": raddr.PubKey.Hex(), "message": string(buf[:n])})
		if err != nil {
			appLog("Failed to marshal json: %v", err)
		}
		select {
		case clientCh <- string(clientMsg):
			appCl.Log().Debugf("Received and sent to ui: %s", clientMsg)
		default:
			appCl.Log().Debugf("Received and trashed: %s", clientMsg)
		}
	}
}

func messageHandler(ctx context.Context) func(w http.ResponseWriter, rreq *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {

		data := map[string]string{}
		if err := json.NewDecoder(req.Body).Decode(&data); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(data["recipient"])); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Determine network type - default to skynet, allow dmsg
		netType := appnet.TypeSkynet
		if network, ok := data["network"]; ok {
			switch network {
			case "dmsg":
				netType = appnet.TypeDmsg
			case "skynet":
				netType = appnet.TypeSkynet
			default:
				http.Error(w, "invalid network type: use 'skynet' or 'dmsg'", http.StatusBadRequest)
				return
			}
		}

		addr := appnet.Addr{
			Net:    netType,
			PubKey: pk,
			Port:   1,
		}
		connsMu.Lock()
		conn, ok := conns[pk]
		connsMu.Unlock()

		if !ok {
			var err error
			err = r.Do(ctx, func() error {
				conn, err = appCl.Dial(addr)
				return err
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			connsMu.Lock()
			conns[pk] = conn
			connsMu.Unlock()

			go handleConn(conn)
		}

		_, err := conn.Write([]byte(data["message"]))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			connsMu.Lock()
			delete(conns, pk)
			connsMu.Unlock()

			return
		}
	}
}

func sseHandler(w http.ResponseWriter, req *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	for {
		select {
		case msg, ok := <-clientCh:
			if !ok {
				return
			}
			_, err := fmt.Fprintf(w, "data: %s\n\n", msg)
			if err == nil {
				f.Flush()
			}

		case <-req.Context().Done():
			appCl.Log().Debug("SSE connection was closed.")
			return
		}
	}
}

func getFileSystem() http.FileSystem {
	fsys, err := fs.Sub(embededFiles, "static")
	if err != nil {
		panic(err)
	}
	return http.FS(fsys)
}

func handleIPCSignal(client *ipc.Client) {
	time.Sleep(5 * time.Second)
	if client == nil {
		appLog("Unable to create IPC Client: server is non-existent")
		return
	}
	for {
		m, err := client.Read()
		if err != nil {
			appLog("%s IPC received error: %v", visorconfig.SkychatName, err)
		}

		if m != nil {
			if m.MsgType == visorconfig.IPCShutdownMessageType {
				appLog("Stopping %s via IPC", visorconfig.SkychatName)
				break
			}
		}

	}
	client.Close()
}

func setAppStatus(appCl *app.Client, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		appCl.Log().Errorf("Failed to set status %v: %v", status, err)
	}
}

func setAppError(appCl *app.Client, appErr error) {
	if err := appCl.SetError(appErr.Error()); err != nil {
		appCl.Log().Errorf("Failed to set error %v: %v", appErr, err)
	}
}

func setAppPort(appCl *app.Client, port routing.Port) {
	if err := appCl.SetAppPort(port); err != nil {
		appCl.Log().Errorf("Failed to set port %v: %v", port, err)
	}
}
