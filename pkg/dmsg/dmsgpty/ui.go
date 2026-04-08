// Package dmsgpty pkg/dmsgpty/ui.go
package dmsgpty

import (
	"bytes"
	stdjson "encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

const (
	wsCols = 100
	wsRows = 30
)

// UIConfig configures the dmsgpty-ui.
type UIConfig struct {
	CmdName string
	CmdArgs []string
}

// DefaultUIConfig returns the default UI config.
func DefaultUIConfig() UIConfig {
	return UIConfig{
		CmdName: DefaultCmd,
		CmdArgs: nil,
	}
}

// UI connects to a dmsgpty-host and exposes a pty via a web UI.
type UI struct {
	log    logrus.FieldLogger
	conf   UIConfig
	dialer UIDialer
}

// NewUI creates a new dmsgpty-ui was a given dailer and config.
func NewUI(dialer UIDialer, conf UIConfig) *UI {
	if dialer == nil {
		panic("NewUI: dialer cannot be nil")
	}
	return &UI{
		log:    logging.MustGetLogger("dmsgpty-ui"),
		conf:   conf,
		dialer: dialer,
	}
}

// Logger returns the internal logger.
func (ui *UI) Logger() logrus.FieldLogger {
	return ui.log
}

// SetLogger sets the internal logger.
// This should be called before serving .Handler()
func (ui *UI) SetLogger(log logrus.FieldLogger) {
	ui.log = log
}

func (ui *UI) writeBanner(w io.Writer, uiAddr string, sID int32) error {
	format := `
██████╗ ███╗   ███╗███████╗ ██████╗ ██████╗ ████████╗██╗   ██╗     ██╗   ██╗██╗
██╔══██╗████╗ ████║██╔════╝██╔════╝ ██╔══██╗╚══██╔══╝╚██╗ ██╔╝     ██║   ██║██║
██║  ██║██╔████╔██║███████╗██║  ███╗██████╔╝   ██║    ╚████╔╝█████╗██║   ██║██║
██║  ██║██║╚██╔╝██║╚════██║██║   ██║██╔═══╝    ██║     ╚██╔╝ ╚════╝██║   ██║██║
██████╔╝██║ ╚═╝ ██║███████║╚██████╔╝██║        ██║      ██║        ╚██████╔╝██║
╚═════╝ ╚═╝     ╚═╝╚══════╝ ╚═════╝ ╚═╝        ╚═╝      ╚═╝         ╚═════╝ ╚═╝
╔═════════════════════════════════════════════════════════════════════════════╗
║ PTY-HOST : %s
║   UI-URL : %s
║   UI-SID : %d
╚═════════════════════════════════════════════════════════════════════════════╝
`
	var b bytes.Buffer
	if _, err := fmt.Fprintf(&b, format, ui.dialer.AddrString(), uiAddr, sID); err != nil {
		return err
	}
	for {
		line, err := b.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return err
		}
		if _, err := w.Write(append(line, '\r')); err != nil {
			return err
		}
	}
}

// Handler returns a http handler that serves the dmsgpty-ui.
func (ui *UI) Handler(customCommands map[string][]string) http.HandlerFunc {
	var sc int32 // session counter
	return func(w http.ResponseWriter, r *http.Request) {
		log := ui.log.WithField("remote_addr", r.RemoteAddr)

		// ensure http method is GET
		if r.Method != http.MethodGet {
			err := fmt.Errorf("http method %s is invalid for path %s", r.Method, r.URL.EscapedPath())
			writeError(log, w, r, err, http.StatusMethodNotAllowed)
			return
		}

		// serve web page
		if !isWebsocket(r.Header) {
			n, err := writeTermHTML(w)
			logrus.WithError(err).
				WithField("bytes", n).
				Debug("Served web page.")
			return
		}

		// serve terminal
		sID := atomic.AddInt32(&sc, 1)
		log = log.WithField("ui_sid", sID)
		log.Debug("Serving terminal websocket...")
		defer func() { log.Debugf("Terminal closed: %d terminals left open.", atomic.AddInt32(&sc, -1)+1) }()

		// open websocket
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			log.WithError(err).Warn("Failed to upgrade to websocket.")
			return
		}
		defer func() { log.WithError(ws.Close(websocket.StatusNormalClosure, "closed")).Debug("Closed ws.") }()

		// Use binary mode for PTY data - text mode fails on non-UTF-8 bytes
		wsConn := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)

		// open pty
		logWS(wsConn, "Dialing...")
		ptyConn, err := ui.dialer.Dial()
		if err != nil {
			writeWSError(log, wsConn, err)
			return
		}
		defer func() { log.WithError(ptyConn.Close()).Debug("Closed ptyConn.") }()

		logWS(wsConn, "Opening pty...")
		ptyC, err := NewPtyClient(ptyConn)
		if err != nil {
			writeWSError(log, wsConn, err)
			return
		}
		defer func() { log.WithError(ptyC.Close()).Debug("Closed ptyC.") }()

		if err = ui.uiStartSize(ptyC); err != nil {
			writeWSError(log, wsConn, err)
			return
		}

		uiAddr := fmt.Sprintf("(%s) %s%s", r.Proto, r.Host, r.URL.Path)
		if err := ui.writeBanner(wsConn, uiAddr, sID); err != nil {
			err := fmt.Errorf("failed to write banner: %w", err)
			writeWSError(log, wsConn, err)
			return
		}

		// websocket keep alive
		go func() {
			for {
				if _, err := wsConn.Write([]byte("\x00")); err != nil {
					return
				}
				time.Sleep(10 * time.Second)
			}
		}()

		// urlCommands from URL | set DMSGPTYTERM=1 all times
		ptyC.Write([]byte(urlCommands(r, customCommands))) //nolint

		// Create WebSocket reader that handles resize messages
		wsReader := newWSReader(ws, ptyC, log, r)
		defer func() {
			if err := wsReader.Close(); err != nil {
				log.WithError(err).Debug("Error closing wsReader")
			}
		}()

		// io
		done, once := make(chan struct{}), new(sync.Once)
		closeDone := func() { once.Do(func() { close(done) }) }
		go func() {
			// Buffer PTY output and flush periodically to reduce WebSocket message count
			bw := newBufferedWSWriter(wsConn, 16*time.Millisecond)
			defer bw.Close()         //nolint:errcheck
			_, _ = io.Copy(bw, ptyC) //nolint:errcheck
			closeDone()
		}()
		go func() {
			_, _ = io.Copy(ptyC, wsReader) //nolint:errcheck
			closeDone()
		}()
		<-done
	}
}

func isWebsocket(h http.Header) bool {
	return h.Get("Upgrade") == "websocket"
}

// ErrorJSON displays errors in JSON format.
type ErrorJSON struct {
	ErrorCode int    `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
}

func logWS(conn net.Conn, msg string) {
	_, _ = fmt.Fprintf(conn, "[dmsgpty-ui] Status: %s\r", msg) //nolint:errcheck
}

func writeWSError(log logrus.FieldLogger, wsConn net.Conn, err error) {
	log.WithError(err).
		WithField("remote_addr", wsConn.RemoteAddr()).
		Error()
	errB := append([]byte("[dmsgpty-ui] Error: "+err.Error()), '\n', '\r')
	if _, err := wsConn.Write(errB); err != nil {
		log.WithError(err).Error("Failed to write error msg to ws conn.")
	}
	logWS(wsConn, "Stopped!")
}

func writeError(log logrus.FieldLogger, w http.ResponseWriter, r *http.Request, err error, code int) {
	log.WithError(err).
		WithField("http_status", code).
		WithField("remote_addr", r.RemoteAddr).
		Error()
	httputil.WriteJSON(w, r, code, ErrorJSON{
		ErrorCode: code,
		ErrorMsg:  err.Error(),
	})
}

func urlCommands(r *http.Request, customCommands map[string][]string) string {
	commands := []string{"export DMSGPTYTERM=1"}
	if commandsQuery, ok := r.URL.Query()["commands"]; ok {
		if len(commandsQuery[0]) > 0 {
			commands = append(commands, strings.Split(commandsQuery[0], ",")...)
		}
	}
	// var commandQuery string
	for i, command := range commands {
		if val, ok := customCommands[command]; ok {
			commands[i] = strings.Join(val, " && ")
		}
	}
	stringCommands := strings.Join(commands, " && ")
	stringCommands += "\n"
	return stringCommands
}

// resizeMsg represents a terminal resize message from the client.
type resizeMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// wsReader reads from a WebSocket connection, handling resize messages separately.
// Resize messages are JSON objects with type="resize", cols, and rows fields.
// All other data is passed through to the PTY.
type wsReader struct {
	ws     *websocket.Conn
	ptyC   *PtyClient
	log    logrus.FieldLogger
	ctx    *http.Request
	closed bool
	mu     sync.Mutex
	buf    []byte // buffered remainder from previous read
}

func newWSReader(ws *websocket.Conn, ptyC *PtyClient, log logrus.FieldLogger, r *http.Request) *wsReader {
	return &wsReader{
		ws:   ws,
		ptyC: ptyC,
		log:  log,
		ctx:  r,
	}
}

func (wr *wsReader) Read(p []byte) (int, error) {
	for {
		wr.mu.Lock()
		if wr.closed {
			wr.mu.Unlock()
			return 0, io.EOF
		}
		// Return buffered remainder from a previous read first.
		if len(wr.buf) > 0 {
			n := copy(p, wr.buf)
			wr.buf = wr.buf[n:]
			if len(wr.buf) == 0 {
				wr.buf = nil
			}
			wr.mu.Unlock()
			return n, nil
		}
		wr.mu.Unlock()

		msgType, data, err := wr.ws.Read(wr.ctx.Context())
		if err != nil {
			return 0, err
		}

		// Try to parse as resize message
		if msgType == websocket.MessageText && len(data) > 0 && data[0] == '{' {
			var msg resizeMsg
			if err := stdjson.Unmarshal(data, &msg); err == nil && msg.Type == "resize" {
				// Handle resize (with bounds checking for uint16)
				if msg.Cols > 0 && msg.Rows > 0 && msg.Cols <= 0xFFFF && msg.Rows <= 0xFFFF {
					size := &WinSize{
						Cols: uint16(msg.Cols), //nolint:gosec // bounds checked above
						Rows: uint16(msg.Rows), //nolint:gosec // bounds checked above
						X:    uint16(msg.Cols), //nolint:gosec // bounds checked above
						Y:    uint16(msg.Rows), //nolint:gosec // bounds checked above
					}
					if err := wr.ptyC.SetPtySize(size); err != nil {
						wr.log.WithError(err).Debug("Failed to set PTY size")
					} else {
						wr.log.WithField("cols", msg.Cols).WithField("rows", msg.Rows).Debug("Resized PTY")
					}
				}
				continue // Don't pass resize message to PTY, read next message
			}
		}

		// Regular data - copy to output buffer, save remainder
		n := copy(p, data)
		if n < len(data) {
			wr.mu.Lock()
			wr.buf = append([]byte(nil), data[n:]...)
			wr.mu.Unlock()
		}
		return n, nil
	}
}

func (wr *wsReader) Close() error {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	wr.closed = true
	return nil
}

// bufferedWSWriter batches writes and flushes them periodically to reduce
// the number of WebSocket messages, improving performance for high-frequency output.
type bufferedWSWriter struct {
	conn     net.Conn
	buf      []byte
	mu       sync.Mutex
	closed   bool
	interval time.Duration
	done     chan struct{}
}

func newBufferedWSWriter(conn net.Conn, flushInterval time.Duration) *bufferedWSWriter {
	bw := &bufferedWSWriter{
		conn:     conn,
		buf:      make([]byte, 0, 4096),
		interval: flushInterval,
		done:     make(chan struct{}),
	}
	go bw.flushLoop()
	return bw
}

func (bw *bufferedWSWriter) Write(p []byte) (int, error) {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	if bw.closed {
		return 0, io.ErrClosedPipe
	}
	bw.buf = append(bw.buf, p...)
	return len(p), nil
}

func (bw *bufferedWSWriter) flushLoop() {
	ticker := time.NewTicker(bw.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			bw.flush()
		case <-bw.done:
			bw.flush() // Final flush
			return
		}
	}
}

func (bw *bufferedWSWriter) flush() {
	bw.mu.Lock()
	if len(bw.buf) == 0 {
		bw.mu.Unlock()
		return
	}
	data := bw.buf
	bw.buf = make([]byte, 0, 4096)
	bw.mu.Unlock()

	_, _ = bw.conn.Write(data) //nolint:errcheck
}

func (bw *bufferedWSWriter) Close() error {
	bw.mu.Lock()
	if bw.closed {
		bw.mu.Unlock()
		return nil
	}
	bw.closed = true
	bw.mu.Unlock()
	close(bw.done)
	return nil
}
