/*
skychat app for skywire visor
*/
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/skycoin/dmsg/buildinfo"
	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/internal/netutil"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

const (
	netType = appnet.TypeSkynet
	port    = routing.Port(1)
)

var addr = flag.String("addr", ":8001", "address to bind")
var r = netutil.NewRetrier(50*time.Millisecond, 5, 2)

var (
	appC     *app.Client
	clientCh chan string
	conns    map[cipher.PubKey]net.Conn // Chat connections
	connsMu  sync.Mutex
)

// the go embed static points to skywire/cmd/apps/skychat/static

//go:embed static
var embededFiles embed.FS

func main() {
	appC = app.NewClient(nil)
	defer appC.Close()

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		fmt.Printf("Failed to output build info: %v", err)
	}

	flag.Parse()
	fmt.Print("Successfully started skychat.")

	clientCh = make(chan string)
	defer close(clientCh)

	conns = make(map[cipher.PubKey]net.Conn)
	go listenLoop()

	http.Handle("/", http.FileServer(getFileSystem()))
	http.HandleFunc("/message", messageHandler)
	http.HandleFunc("/sse", sseHandler)

	fmt.Print("Serving HTTP on", *addr)
	err := http.ListenAndServe(*addr, nil)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func listenLoop() {
	l, err := appC.Listen(netType, port)
	if err != nil {
		fmt.Printf("Error listening network %v on port %d: %v\n", netType, port, err)
		return
	}

	for {
		fmt.Print("Accepting skychat conn...")
		conn, err := l.Accept()
		if err != nil {
			fmt.Print("Failed to accept conn:", err)
			return
		}
		fmt.Print("Accepted skychat conn")

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
			fmt.Print("Failed to read packet:", err)
			raddr := conn.RemoteAddr().(appnet.Addr)
			connsMu.Lock()
			delete(conns, raddr.PubKey)
			connsMu.Unlock()
			return
		}

		clientMsg, err := json.Marshal(map[string]string{"sender": raddr.PubKey.Hex(), "message": string(buf[:n])})
		if err != nil {
			fmt.Printf("Failed to marshal json: %v", err)
		}
		select {
		case clientCh <- string(clientMsg):
			fmt.Printf("Received and sent to ui: %s\n", clientMsg)
		default:
			fmt.Printf("Received and trashed: %s\n", clientMsg)
		}
	}
}

func messageHandler(w http.ResponseWriter, req *http.Request) {
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
		err = r.Do(func() error {
			conn, err = appC.Dial(addr)
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
