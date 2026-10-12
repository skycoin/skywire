// Package autoconfigui pkg/skywireconfig/autoconfigui/web.go c4-vis-cli
package autoconfigui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed" // for the page
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
)

//go:embed page.html
var pageHTML []byte

// ApplyFunc runs autoconfig with the given flag arguments and returns what it
// printed. The command wires in the real autoconfig, tests inject their own.
type ApplyFunc func(args []string) (string, error)

// NewToken returns a random one-time access token.
func NewToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Web serves the form over HTTP.
type Web struct {
	Token string
	Path  string
	Apply ApplyFunc
	Flags func() []autoconfigcmd.Flag

	mu sync.Mutex
}

// NewWeb returns a handler set serving the autoconfig form for the skyenv
// file at path.
func NewWeb(token, path string, apply ApplyFunc) *Web {
	return &Web{Token: token, Path: path, Apply: apply, Flags: autoconfigcmd.Describe}
}

type request struct {
	Values    map[string]string `json:"values"`
	NoRestart bool              `json:"no_restart"`
}

type response struct {
	Command string `json:"command,omitempty"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
	Model   *Model `json:"model,omitempty"`
}

func (w *Web) authorized(r *http.Request) bool {
	tok := r.Header.Get("X-Token")
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(w.Token)) == 1
}

// Handler returns the HTTP handler. Every route requires the token.
func (w *Web) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", w.page)
	mux.HandleFunc("/api/model", w.model)
	mux.HandleFunc("/api/print", w.print)
	mux.HandleFunc("/api/apply", w.apply)
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !w.authorized(r) {
			http.Error(rw, "token required", http.StatusUnauthorized)
			return
		}
		rw.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(rw, r)
	})
}

func (w *Web) page(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write(pageHTML) //nolint:errcheck
}

func (w *Web) fresh() *Model { return NewModel(w.Flags(), w.Path) }

func (w *Web) model(rw http.ResponseWriter, _ *http.Request) {
	writeJSON(rw, http.StatusOK, response{Model: w.fresh()})
}

// edited builds a model from the file and overlays the request's values.
func (w *Web) edited(rw http.ResponseWriter, r *http.Request) (*Model, error) {
	var req request
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(&req); err != nil {
		return nil, err
	}
	m := w.fresh()
	for name, v := range req.Values {
		if err := m.Set(name, v); err != nil {
			return nil, err
		}
	}
	m.NoRestart = req.NoRestart
	return m, nil
}

func (w *Web) print(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST required", http.StatusMethodNotAllowed)
		return
	}
	m, err := w.edited(rw, r)
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, response{Error: err.Error()})
		return
	}
	cmd, err := m.Command()
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, response{Error: err.Error()})
		return
	}
	writeJSON(rw, http.StatusOK, response{Command: cmd})
}

func (w *Web) apply(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST required", http.StatusMethodNotAllowed)
		return
	}
	m, err := w.edited(rw, r)
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, response{Error: err.Error()})
		return
	}
	args, err := m.Args()
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, response{Error: err.Error()})
		return
	}
	w.mu.Lock()
	out, err := w.Apply(args)
	w.mu.Unlock()
	resp := response{Output: out, Model: w.fresh()}
	if err != nil {
		resp.Error = err.Error()
		writeJSON(rw, http.StatusInternalServerError, resp)
		return
	}
	writeJSON(rw, http.StatusOK, resp)
}

func writeJSON(rw http.ResponseWriter, code int, v interface{}) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(v) //nolint:errcheck
}

// Serve listens on addr until ctx ends. ready receives the URL to open, token
// included, once the listener is up.
func (w *Web) Serve(ctx context.Context, addr string, ready func(url string)) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: w.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close() //nolint:errcheck
	}()
	ready("http://" + ln.Addr().String() + "/?token=" + w.Token)
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}
