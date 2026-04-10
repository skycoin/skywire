// Package commands cmd/apps/skychat/skychat.go
package commands

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	ipc "github.com/james-barrow/golang-ipc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/cmd/apps/skychat/history"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
)

var r = netutil.NewRetrier(nil, 50*time.Millisecond, netutil.DefaultMaxBackoff, 5, 2)

var (
	addr      string
	appCl     *app.Client
	appLog    func(format string, args ...interface{}) // App logger function
	clientCh  chan string
	conns     map[cipher.PubKey]net.Conn // Chat connections
	connsMu   sync.Mutex
	appPort   uint16
	useSkynet bool
	useDmsg   bool

	// Persistence (Phase 1) — all off by default.
	persistEnabled       bool
	persistDBPath        string
	persistMaxMsgSize    int
	persistPerPeerRate   int
	persistPerPeerCap    int
	persistTotalCapMB    int
	persistTTLDays       int
	persistWhitelistFile string
	persistSeedCount     int

	// historyStore is nil when persistence is disabled.
	historyStore history.Store
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

	// Persistence flags (Phase 1). All default off; when --persist is set,
	// the others fall back to conservative defaults.
	RootCmd.Flags().BoolVar(&persistEnabled, "persist", false, "persist chat history to a local BoltDB (off by default)")
	RootCmd.Flags().StringVar(&persistDBPath, "persist-db", "", "path to the BoltDB file (default: <work-dir>/skychat-history.db)")
	RootCmd.Flags().IntVar(&persistMaxMsgSize, "persist-max-size", 4096, "maximum persisted message size in bytes")
	RootCmd.Flags().IntVar(&persistPerPeerRate, "persist-per-peer-rate", 20, "persisted messages per minute per peer (rate limit)")
	RootCmd.Flags().IntVar(&persistPerPeerCap, "persist-per-peer-cap", 500, "maximum persisted messages per peer (FIFO eviction)")
	RootCmd.Flags().IntVar(&persistTotalCapMB, "persist-total-cap", 10, "total persisted storage cap in MB")
	RootCmd.Flags().IntVar(&persistTTLDays, "persist-ttl", 30, "days to keep persisted messages before sweep (0 disables)")
	RootCmd.Flags().StringVar(&persistWhitelistFile, "persist-whitelist", "", "path to file with one peer PK per line; if set, only these peers are persisted")
	RootCmd.Flags().IntVar(&persistSeedCount, "persist-seed", 50, "number of recent messages to seed new SSE clients with (0 disables)")
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
		fs.BoolVar(&persistEnabled, "persist", false, "persist chat history to BoltDB")
		fs.StringVar(&persistDBPath, "persist-db", "", "path to BoltDB file")
		fs.IntVar(&persistMaxMsgSize, "persist-max-size", 4096, "max message size bytes")
		fs.IntVar(&persistPerPeerRate, "persist-per-peer-rate", 20, "per-peer rate limit / min")
		fs.IntVar(&persistPerPeerCap, "persist-per-peer-cap", 500, "per-peer message cap")
		fs.IntVar(&persistTotalCapMB, "persist-total-cap", 10, "total storage cap in MB")
		fs.IntVar(&persistTTLDays, "persist-ttl", 30, "days to keep persisted messages")
		fs.StringVar(&persistWhitelistFile, "persist-whitelist", "", "whitelist file path")
		fs.IntVar(&persistSeedCount, "persist-seed", 50, "messages to seed SSE clients with")
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

	if persistEnabled {
		if err := openHistoryStore(); err != nil {
			appLog("Failed to open history store: %v — continuing in ephemeral mode", err)
		} else {
			defer func() {
				if historyStore != nil {
					if err := historyStore.Close(); err != nil {
						appLog("history store close: %v", err)
					}
				}
			}()
		}
	}

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
		ipcClient, err := ipc.StartClient(skyenv.SkychatName, nil)
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
	http.HandleFunc("/history", historyHandler)
	http.HandleFunc("/history/peers", historyPeersHandler)

	url := ""
	address := addr
	if len(address) >= 2 && address[:2] == "*:" {
		url = "0.0.0.0" + address[1:]
	} else if len(address) >= 1 && address[:1] == ":" {
		url = "127.0.0.1" + address
	} else if host, port, err := net.SplitHostPort(address); err == nil && host != "" && port != "" {
		url = address
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

		text := string(buf[:n])
		peerPK := raddr.PubKey.Hex()

		// Persist (best-effort, never blocks ephemeral path).
		persistMessage(history.Message{
			Peer:      peerPK,
			From:      peerPK,
			Outgoing:  false,
			Text:      text,
			Timestamp: time.Now().UTC(),
		})

		clientMsg, err := json.Marshal(map[string]string{"sender": peerPK, "message": text})
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

		// Persist outgoing message (best-effort).
		persistMessage(history.Message{
			Peer:      pk.Hex(),
			Outgoing:  true,
			Text:      data["message"],
			Timestamp: time.Now().UTC(),
		})
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

	// Seed the new SSE client with recent history if persistence is enabled.
	if historyStore != nil && persistSeedCount > 0 {
		recent, err := historyStore.ListRecent(persistSeedCount)
		if err != nil {
			appCl.Log().Debugf("SSE seed list failed: %v", err)
		} else {
			for _, m := range recent {
				sender := m.From
				if m.Outgoing {
					sender = "self"
				}
				b, _ := json.Marshal(map[string]string{ //nolint:errcheck,gosec
					"sender":  sender,
					"message": m.Text,
					"peer":    m.Peer,
					"ts":      m.Timestamp.Format(time.RFC3339),
					"history": "true",
				})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b) //nolint:errcheck,gosec
			}
			f.Flush()
		}
	}

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

// historyHandler returns JSON history. Query params:
//
//	peer=<pk>    — filter to a specific peer
//	limit=<int>  — max messages to return (default 100, max 1000)
func historyHandler(w http.ResponseWriter, req *http.Request) {
	if historyStore == nil {
		http.Error(w, "persistence not enabled", http.StatusServiceUnavailable)
		return
	}
	peer := req.URL.Query().Get("peer")
	limit := 100
	if v := req.URL.Query().Get("limit"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &limit); err != nil || limit <= 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	var msgs []history.Message
	var err error
	if peer != "" {
		msgs, err = historyStore.ListByPeer(peer, limit)
	} else {
		msgs, err = historyStore.ListRecent(limit)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msgs) //nolint:errcheck,gosec
}

func historyPeersHandler(w http.ResponseWriter, _ *http.Request) {
	if historyStore == nil {
		http.Error(w, "persistence not enabled", http.StatusServiceUnavailable)
		return
	}
	peers, err := historyStore.Peers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(peers) //nolint:errcheck,gosec
}

// persistMessage stores a message in the history backend if persistence is
// enabled. Errors are logged at debug level; ephemeral delivery is never
// blocked by persistence failure.
func persistMessage(msg history.Message) {
	if historyStore == nil {
		return
	}
	if err := historyStore.Append(msg); err != nil {
		switch {
		case errors.Is(err, history.ErrRateLimited),
			errors.Is(err, history.ErrTooLarge),
			errors.Is(err, history.ErrStorageFull),
			errors.Is(err, history.ErrNotWhitelisted):
			appCl.Log().Debugf("history: dropped %s (%v)", msg.Peer, err)
		default:
			appLog("history: backend error: %v", err)
		}
	}
}

// openHistoryStore constructs the bolt history store from CLI flags.
func openHistoryStore() error {
	dbPath := persistDBPath
	if dbPath == "" {
		workDir := appCl.Config().ProcWorkDir
		if workDir == "" {
			workDir = skyenv.LocalPath
		}
		dbPath = filepath.Join(workDir, "skychat-history.db")
	}

	limits := history.Limits{
		MaxMessageSize:    persistMaxMsgSize,
		PerPeerRatePerMin: persistPerPeerRate,
		PerPeerCap:        persistPerPeerCap,
		TotalCapBytes:     int64(persistTotalCapMB) * 1024 * 1024,
		TTL:               time.Duration(persistTTLDays) * 24 * time.Hour,
	}
	if persistWhitelistFile != "" {
		wl, err := loadWhitelist(persistWhitelistFile)
		if err != nil {
			return fmt.Errorf("load whitelist: %w", err)
		}
		limits.WhitelistOnly = true
		limits.Whitelist = wl
	}

	s, err := history.NewBoltStore(dbPath, limits)
	if err != nil {
		return err
	}
	historyStore = s
	appLog("Persistence enabled: db=%s cap=%dMB per-peer=%d ttl=%dd whitelist=%v",
		dbPath, persistTotalCapMB, persistPerPeerCap, persistTTLDays, limits.WhitelistOnly)
	return nil
}

// loadWhitelist reads a file with one peer PK hex per line (ignoring blanks
// and lines starting with #).
func loadWhitelist(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	wl := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		wl[line] = true
	}
	return wl, nil
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
			appLog("%s IPC received error: %v", skyenv.SkychatName, err)
		}

		if m != nil {
			if m.MsgType == skyenv.IPCShutdownMessageType {
				appLog("Stopping %s via IPC", skyenv.SkychatName)
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
