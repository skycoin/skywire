package dmsgpty

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/creack/pty"
	"github.com/sirupsen/logrus"
	"nhooyr.io/websocket"

	"github.com/SkycoinProject/dmsg/httputil"
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
		CmdName: "/bin/bash",
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
		panic(err)
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
func (ui *UI) Handler() http.HandlerFunc {
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
				Info("Served web page.")
			return
		}

		// serve terminal
		sID := atomic.AddInt32(&sc, 1)
		log = log.WithField("ui_sid", sID)
		log.Info("Serving terminal websocket...")
		defer func() { log.Infof("Terminal closed: %d terminals left open.", atomic.AddInt32(&sc, -1)+1) }()

		// open pty
		ptyConn, err := ui.dialer.Dial()
		if err != nil {
			writeError(log, w, r, err, http.StatusServiceUnavailable)
			return
		}
		defer func() { log.WithError(ptyConn.Close()).Debug("Closed ptyConn.") }()

		ptyC, err := NewPtyClient(ptyConn)
		if err != nil {
			writeError(log, w, r, err, http.StatusServiceUnavailable)
			return
		}
		defer func() { log.WithError(ptyC.Close()).Debug("Closed ptyC.") }()

		if err := ptyC.StartWithSize(ui.conf.CmdName, ui.conf.CmdArgs, &pty.Winsize{Rows: wsRows, Cols: wsCols}); err != nil {
			writeError(log, w, r, err, http.StatusServiceUnavailable)
			return
		}

		// open websocket
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			log.WithError(err).Warn("Failed to upgrade to websocket.")
			return
		}
		defer func() { log.WithError(ws.Close(websocket.StatusNormalClosure, "closed")).Debug("Closed ws.") }()

		wsConn := websocket.NetConn(r.Context(), ws, websocket.MessageText)

		uiAddr := fmt.Sprintf("(%s) %s%s", r.Proto, r.Host, r.URL.Path)
		if err := ui.writeBanner(wsConn, uiAddr, sID); err != nil {
			log.WithError(err).Warn("Failed to write banner.")
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

		// io
		done, once := make(chan struct{}), new(sync.Once)
		closeDone := func() { once.Do(func() { close(done) }) }
		go func() {
			_, _ = io.Copy(wsConn, ptyC) //nolint:errcheck
			closeDone()
		}()
		go func() {
			_, _ = io.Copy(ptyC, wsConn) //nolint:errcheck
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
