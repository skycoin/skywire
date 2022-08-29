/*
skychat app for skywire visor
*/
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"time"

	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	netType = appnet.TypeSkynet
	port    = routing.Port(1)
)

var addr = flag.String("addr", ":8001", "address to bind")
var r = netutil.NewRetrier(nil, 50*time.Millisecond, netutil.DefaultMaxBackoff, 5, 2)

var (
	appCl    *app.Client
	clientCh chan string
	conns    map[cipher.PubKey]net.Conn // Chat connections
	connsMu  sync.Mutex
)

// the go embed static points to skywire/cmd/apps/skychat/static

//go:embed static
var embededFiles embed.FS

func main() {
	appCl = app.NewClient(nil)
	defer appCl.Close()

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		print(fmt.Sprintf("Failed to output build info: %v\n", err))
	}

	flag.Parse()
	fmt.Println("Successfully started skychat.")

	clientCh = make(chan string)
	defer close(clientCh)

	conns = make(map[cipher.PubKey]net.Conn)
	go listenLoop()

	if runtime.GOOS == "windows" {
		ipcClient, err := ipc.StartClient(skyenv.SkychatName, nil)
		if err != nil {
			print(fmt.Sprintf("Error creating ipc server for skychat client: %v\n", err))
			setAppError(appCl, err)
			os.Exit(1)
		}
		go handleIPCSignal(ipcClient)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	http.Handle("/", http.FileServer(getFileSystem()))
	http.HandleFunc("/message", messageHandler(ctx))
	http.HandleFunc("/sse", sseHandler)

	fmt.Println("Serving HTTP on", *addr)
	if runtime.GOOS != "windows" {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)

		go func() {
			<-termCh
			setAppStatus(appCl, appserver.AppDetailedStatusStopped)
			os.Exit(1)
		}()
	}
	setAppStatus(appCl, appserver.AppDetailedStatusRunning)
	srv := &http.Server{ //nolint gosec
		Addr:         *addr,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	err := srv.ListenAndServe()
	if err != nil {
		print(err.Error())
		setAppError(appCl, err)
		os.Exit(1)
	}

}

func listenLoop() {
	l, err := appCl.Listen(netType, port)
	if err != nil {
		print(fmt.Sprintf("Error listening network %v on port %d: %v\n", netType, port, err))
		setAppError(appCl, err)
		return
	}

	for {
		fmt.Println("Accepting skychat conn...")
		conn, err := l.Accept()
		if err != nil {
			print(fmt.Sprintf("Failed to accept conn: %v\n", err))
			return
		}
		fmt.Println("Accepted skychat conn")

		raddr := conn.RemoteAddr().(appnet.Addr)
		connsMu.Lock()
		conns[raddr.PubKey] = conn
		connsMu.Unlock()
		fmt.Printf("Accepted skychat conn on %s from %s\n", conn.LocalAddr(), raddr.PubKey)

		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	raddr := conn.RemoteAddr().(appnet.Addr)
	for {
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			fmt.Println("Failed to read packet:", err)
			raddr := conn.RemoteAddr().(appnet.Addr)
			connsMu.Lock()
			delete(conns, raddr.PubKey)
			connsMu.Unlock()
			return
		}

		clientMsg, err := json.Marshal(map[string]string{"sender": raddr.PubKey.Hex(), "message": string(buf[:n])})
		if err != nil {
			print(fmt.Sprintf("Failed to marshal json: %v\n", err))
		}
		select {
		case clientCh <- string(clientMsg):
			fmt.Printf("Received and sent to ui: %s\n", clientMsg)
		default:
			fmt.Printf("Received and trashed: %s\n", clientMsg)
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
			_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
			f.Flush()

		case <-req.Context().Done():
			fmt.Print("SSE connection were closed.")
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

func setAppStatus(appCl *app.Client, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		print(fmt.Sprintf("Failed to set status %v: %v\n", status, err))
	}
}

func setAppError(appCl *app.Client, appErr error) {
	if err := appCl.SetError(appErr.Error()); err != nil {
		print(fmt.Sprintf("Failed to set error %v: %v\n", appErr, err))
	}
}
