//go:generate esc -o static.go -prefix static static

/*
skychat app for skywire visor
*/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/internal/netutil"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/util/buildinfo"
)

const (
	appName = "skychat"
	netType = appnet.TypeSkynet
	port    = routing.Port(1)
)

var addr = flag.String("addr", ":8001", "address to bind")
var r = netutil.NewRetrier(50*time.Millisecond, 5, 2)

var (
	chatApp   *app.Client
	clientCh  chan string
	chatConns map[cipher.PubKey]net.Conn
	connsMu   sync.Mutex
	log       *logging.MasterLogger
)

func main() {
	log = app.NewLogger(appName)
	flag.Parse()

	if _, err := buildinfo.Get().WriteTo(log.Writer()); err != nil {
		log.Printf("Failed to output build info: %v", err)
	}

	clientConfig, err := app.ClientConfigFromEnv()
	if err != nil {
		log.Fatalf("Error getting client config: %v\n", err)
	}

	// TODO: pass `log`?
	a, err := app.NewClient(logging.MustGetLogger(fmt.Sprintf("app_%s", appName)), clientConfig)
	if err != nil {
		log.Fatal("Setup failure: ", err)
	}
	defer a.Close()
	log.Println("Successfully created skychat app")

	chatApp = a

	clientCh = make(chan string)
	defer close(clientCh)

	chatConns = make(map[cipher.PubKey]net.Conn)
	go listenLoop()

	http.Handle("/", http.FileServer(FS(false)))
	http.HandleFunc("/message", messageHandler)
	http.HandleFunc("/sse", sseHandler)

	log.Println("Serving HTTP on", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func listenLoop() {
	l, err := chatApp.Listen(netType, port)
	if err != nil {
		log.Printf("Error listening network %v on port %d: %v\n", netType, port, err)
		return
	}

	for {
		log.Println("Accepting skychat conn...")
		conn, err := l.Accept()
		if err != nil {
			log.Println("Failed to accept conn:", err)
			return
		}
		log.Println("Accepted skychat conn")

		raddr := conn.RemoteAddr().(appnet.Addr)
		connsMu.Lock()
		chatConns[raddr.PubKey] = conn
		connsMu.Unlock()
		log.Printf("Accepted skychat conn on %s from %s\n", conn.LocalAddr(), raddr.PubKey)

		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	raddr := conn.RemoteAddr().(appnet.Addr)
	for {
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			log.Println("Failed to read packet:", err)
			raddr := conn.RemoteAddr().(appnet.Addr)
			connsMu.Lock()
			delete(chatConns, raddr.PubKey)
			connsMu.Unlock()
			return
		}

		clientMsg, err := json.Marshal(map[string]string{"sender": raddr.PubKey.Hex(), "message": string(buf[:n])})
		if err != nil {
			log.Printf("Failed to marshal json: %v", err)
		}
		select {
		case clientCh <- string(clientMsg):
			log.Printf("Received and sent to ui: %s\n", clientMsg)
		default:
			log.Printf("Received and trashed: %s\n", clientMsg)
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
	conn, ok := chatConns[pk]
	connsMu.Unlock()

	if !ok {
		var err error
		err = r.Do(func() error {
			conn, err = chatApp.Dial(addr)
			return err
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		connsMu.Lock()
		chatConns[pk] = conn
		connsMu.Unlock()

		go handleConn(conn)
	}

	_, err := conn.Write([]byte(data["message"]))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		connsMu.Lock()
		delete(chatConns, pk)
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
			log.Println("SSE connection were closed.")
			return
		}
	}
}
