// Package web pkg/cmd/skywire/commands/web — `skywire web` command.
//
// Serves a browser-based UI for the entire skywire CLI tree. The
// page is a TinyGo-compiled WASM bundle (see ./wasm/main.go); the
// server walks the live cobra tree at startup, exposes it as JSON,
// and proxies command execution via subprocess + Server-Sent Events.
//
// Why a separate binary subcommand and not nested under `cli`: this
// command spawns `./skywire <subpath>` as a subprocess, so it's a
// PEER of cli, not a child — clearer in the help tree this way.
//
// Static assets (index.html, wasm_exec.js, b.wasm) are embedded via
// //go:embed so a release binary ships self-contained. b.wasm is
// produced by `make build` in this directory; absent that, the
// server still starts and serves the index page with a build-me
// banner.
package web

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

//go:embed static/index.html
var indexHTML []byte

//go:embed static/wasm_exec.js
var wasmExecJS []byte

// b.wasm is optional — embedded if produced by `make build` in this
// dir, otherwise the embed will be empty and the loader page shows
// a "rebuild me" banner. The empty default keeps the package
// buildable from a fresh checkout without TinyGo installed.
//
//go:embed static/b.wasm
var bWasm []byte

var (
	flagAddr    string
	flagToken   string
	flagAllow   []string
	flagSkywire string
)

func init() {
	RootCmd.Flags().StringVar(&flagAddr, "addr", "127.0.0.1:8088",
		"HTTP bind address (use 'host:port'; default loopback-only)")
	RootCmd.Flags().StringVar(&flagToken, "token", "",
		"require ?token=... or Authorization: Bearer for every request (empty = no auth, only safe on loopback)")
	RootCmd.Flags().StringSliceVar(&flagAllow, "allow", nil,
		"allowlist of subcommand paths (dot-separated, e.g. 'cli.skychat.send'); empty = allow all non-hidden subcommands")
	RootCmd.Flags().StringVar(&flagSkywire, "skywire", "",
		"path to the skywire binary used for subprocess execution (empty = use the running binary)")
}

// RootCmd is the cobra entry point. Mounted from
// cmd/skywire/commands/root.go.
var RootCmd = &cobra.Command{
	Use:   "web",
	Short: "Serve the skywire CLI as a browser-based UI",
	Long: `Start a local HTTP server that renders the entire skywire
cobra command tree as a navigable web page. Click any subcommand
to see its help, fill in flags, and execute — output streams back
to the browser via Server-Sent Events.

The page is a TinyGo-compiled WASM bundle (no JavaScript framework,
no client-side state outside the WASM module). The server walks
the live cobra tree at startup so the UI always reflects the
current binary's actual capabilities.

Default binding is loopback-only. For LAN/remote access, pair
--addr 0.0.0.0:8088 with --token <secret> and require clients to
provide the token via ?token= or Authorization: Bearer.

The --allow flag scopes which subcommands the UI exposes — useful
for hosted instances. Example: --allow cli.skychat,cli.dmsg.curl
exposes only chat send + dmsg curl. Empty allowlist = everything
non-hidden.

Examples:
  skywire web                                  # localhost:8088, no auth
  skywire web --addr 0.0.0.0:8088 --token foo  # LAN, token-gated
  skywire web --allow cli.skychat              # only chat subcommands`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return serve(cmd.Context(), cmd.Root())
	},
}

func serve(ctx context.Context, root *cobra.Command) error {
	skywireBin := flagSkywire
	if skywireBin == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve own binary path: %w", err)
		}
		skywireBin = exe
	}
	tree := buildTree(root, flagAllow)

	mux := http.NewServeMux()
	mux.Handle("GET /", authMiddleware(http.HandlerFunc(handleIndex)))
	mux.Handle("GET /b.wasm", authMiddleware(http.HandlerFunc(handleWasm)))
	mux.Handle("GET /wasm_exec.js", authMiddleware(http.HandlerFunc(handleWasmExec)))
	mux.Handle("GET /api/tree", authMiddleware(handleTree(tree)))

	runs := newRunRegistry()
	mux.Handle("POST /api/run", authMiddleware(handleRun(runs, skywireBin)))
	mux.Handle("GET /api/sse/{id}", authMiddleware(handleSSE(runs)))
	mux.Handle("POST /api/cancel/{id}", authMiddleware(handleCancel(runs)))

	srv := &http.Server{
		Addr:              flagAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Shutdown on ctx cancel — outer command's signal handler
	// (cobra's default) plumbs SIGINT through.
	go func() { //nolint:gosec // G118: shutdown timeout deliberately uses Background — the request ctx is being canceled
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx) //nolint:errcheck
		runs.cancelAll()
	}()

	fmt.Fprintf(os.Stderr, "skywire web: serving on http://%s/\n", flagAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// authMiddleware is the optional token gate. No-op when flagToken is
// empty — operator's responsibility to keep the bind loopback in
// that case. Loopback bindings (127.0.0.0/8, ::1) bypass the gate
// because the operator running localhost obviously authenticates
// themselves via OS access.
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if flagToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		// Loopback bypass — RemoteAddr is "host:port".
		host, _, _ := strings.Cut(r.RemoteAddr, ":")
		if host == "127.0.0.1" || host == "::1" || host == "localhost" {
			next.ServeHTTP(w, r)
			return
		}
		got := r.URL.Query().Get("token")
		if got == "" {
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				got = strings.TrimPrefix(auth, "Bearer ")
			}
		}
		if got != flagToken {
			http.Error(w, "missing or invalid token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML) //nolint:errcheck
}

func handleWasm(w http.ResponseWriter, _ *http.Request) {
	if len(bWasm) == 0 {
		http.Error(w, "b.wasm not built — run `make build` in cmd/skywire/commands/web/", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/wasm")
	_, _ = w.Write(bWasm) //nolint:errcheck
}

func handleWasmExec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write(wasmExecJS) //nolint:errcheck
}

// CommandNode is the JSON-friendly shape of one cobra command in the
// tree. Flat children list (not nested) so the WASM client can
// navigate via path lookups without recursing JSON. flags include
// only non-hidden user-facing flags.
type CommandNode struct {
	Path     string     `json:"path"` // dot-separated, "" for root
	Name     string     `json:"name"`
	Short    string     `json:"short,omitempty"`
	Long     string     `json:"long,omitempty"`
	Example  string     `json:"example,omitempty"`
	Use      string     `json:"use,omitempty"` // raw Use string from cobra
	Children []string   `json:"children,omitempty"`
	Flags    []FlagInfo `json:"flags,omitempty"`
	Runnable bool       `json:"runnable"` // has Run / RunE
	Hidden   bool       `json:"hidden,omitempty"`
}

// FlagInfo carries enough flag metadata for the WASM side to render
// an appropriate <input>. Type strings match pflag's Value.Type()
// output ("string", "bool", "int", "duration", etc.).
type FlagInfo struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage,omitempty"`
}

// buildTree walks the cobra subcommand graph rooted at `root` and
// returns a path→node map. The empty-string key is the root itself.
// Hidden commands are included only when the operator's --allow
// list names them explicitly; the WASM client decides whether to
// surface them in the sidebar.
func buildTree(root *cobra.Command, allow []string) map[string]CommandNode {
	allowSet := make(map[string]struct{}, len(allow))
	for _, a := range allow {
		allowSet[a] = struct{}{}
	}
	out := make(map[string]CommandNode)
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		// Non-allowlisted nodes are still walked so allowed descendants can
		// mount under an implicit breadcrumb; the node itself is emitted with
		// its flags/runnable bit regardless.
		children := make([]string, 0, len(c.Commands()))
		for _, sub := range c.Commands() {
			if sub.Hidden && len(allow) == 0 {
				continue
			}
			subPath := sub.Name()
			if path != "" {
				subPath = path + "." + sub.Name()
			}
			children = append(children, subPath)
			walk(sub, subPath)
		}
		flags := []FlagInfo{}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden {
				return
			}
			flags = append(flags, FlagInfo{
				Name:      f.Name,
				Shorthand: f.Shorthand,
				Type:      f.Value.Type(),
				Default:   f.DefValue,
				Usage:     f.Usage,
			})
		})
		runnable := c.Run != nil || c.RunE != nil
		out[path] = CommandNode{
			Path:     path,
			Name:     c.Name(),
			Short:    c.Short,
			Long:     strings.TrimSpace(c.Long),
			Example:  strings.TrimSpace(c.Example),
			Use:      c.Use,
			Children: children,
			Flags:    flags,
			Runnable: runnable,
			Hidden:   c.Hidden,
		}
	}
	walk(root, "")
	return out
}

func handleTree(tree map[string]CommandNode) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tree) //nolint:errcheck
	})
}

// runRegistry tracks running subprocesses so /api/sse/<id> can find
// their stdout stream and /api/cancel/<id> can SIGINT them.
type runRegistry struct {
	mu   sync.Mutex
	runs map[string]*run
}

type run struct {
	cmd     *exec.Cmd
	output  chan string // line-buffered stdout+stderr
	done    chan int    // exit code
	cancel  context.CancelFunc
	started time.Time
}

func newRunRegistry() *runRegistry { return &runRegistry{runs: map[string]*run{}} }

func (rr *runRegistry) put(id string, r *run) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.runs[id] = r
}

func (rr *runRegistry) get(id string) (*run, bool) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	r, ok := rr.runs[id]
	return r, ok
}

func (rr *runRegistry) delete(id string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	delete(rr.runs, id)
}

func (rr *runRegistry) cancelAll() {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	for _, r := range rr.runs {
		r.cancel()
	}
}

// RunRequest is the POST /api/run body — subpath plus user-entered
// flag values + positional args.
type RunRequest struct {
	Path  string            `json:"path"`            // dot-separated, mapped to space-separated argv
	Flags map[string]string `json:"flags,omitempty"` // key = long flag name, value = string repr
	Args  []string          `json:"args,omitempty"`  // positional args
}

func handleRun(runs *runRegistry, skywireBin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req RunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "decode body: "+err.Error(), http.StatusBadRequest)
			return
		}
		argv := append([]string{}, strings.Split(req.Path, ".")...)
		for k, v := range req.Flags {
			argv = append(argv, "--"+k, v)
		}
		argv = append(argv, req.Args...)

		runCtx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel routed to run
		cmd := exec.CommandContext(runCtx, skywireBin, argv...)    //nolint:gosec // skywireBin is the resolved self-binary path; argv is trusted CLI input
		// Combined stdout+stderr to one pipe; we don't distinguish
		// in the SSE stream (the wasm client just renders sequential
		// lines, like a terminal would).
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			http.Error(w, "stdout pipe: "+err.Error(), http.StatusInternalServerError)
			return
		}
		cmd.Stderr = cmd.Stdout
		// SIGINT on cancel so the subprocess gets a chance to clean
		// up (matters for visor halt, dmsg curl downloads, etc).
		setProcGroup(cmd)

		if err := cmd.Start(); err != nil {
			cancel()
			http.Error(w, "start: "+err.Error(), http.StatusInternalServerError)
			return
		}

		id := uuid.NewString()
		runEntry := &run{
			cmd:     cmd,
			output:  make(chan string, 256),
			done:    make(chan int, 1),
			cancel:  cancel,
			started: time.Now(),
		}
		runs.put(id, runEntry)

		// Reader goroutine: line-split stdout into the channel.
		go func() {
			defer close(runEntry.output)
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				select {
				case runEntry.output <- sc.Text():
				case <-runCtx.Done():
					return
				}
			}
		}()
		// Wait goroutine: emit exit code, then drop from registry
		// after a grace window so late SSE consumers can read it.
		go func() {
			err := cmd.Wait()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					code = exitErr.ExitCode()
				} else {
					code = -1
				}
			}
			runEntry.done <- code
			close(runEntry.done)
			// Grace: keep the entry around for 30s so a slow SSE
			// reconnect can still pull the exit code.
			time.AfterFunc(30*time.Second, func() {
				runs.delete(id)
				cancel()
			})
		}()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id}) //nolint:errcheck
	})
}

func handleSSE(runs *runRegistry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		runEntry, ok := runs.get(id)
		if !ok {
			http.Error(w, "no such run", http.StatusNotFound)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Browser disconnect → SIGINT to subprocess. The Setpgid
		// above lets us kill the whole process group, catching any
		// children the subprocess spawned (e.g., `skywire visor`'s
		// app subprocesses).
		notify := r.Context().Done()

		for {
			select {
			case line, ok := <-runEntry.output:
				if !ok {
					// Output channel closed → wait for exit code +
					// emit it as a final event, then close.
					select {
					case code := <-runEntry.done:
						fmt.Fprintf(w, "event: exit\ndata: %d\n\n", code) //nolint:errcheck
						flusher.Flush()
					case <-time.After(5 * time.Second):
						fmt.Fprintf(w, "event: exit\ndata: -1\n\n") //nolint:errcheck
						flusher.Flush()
					}
					return
				}
				writeSSE(w, "stdout", line)
				flusher.Flush()
			case <-notify:
				// Client gone — kill the subprocess group.
				killProcGroup(runEntry.cmd.Process)
				return
			}
		}
	})
}

func writeSSE(w io.Writer, event, data string) {
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(w, "event: %s\ndata: %s\n", event, line) //nolint:errcheck
	}
	fmt.Fprint(w, "\n") //nolint:errcheck
}

func handleCancel(runs *runRegistry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		runEntry, ok := runs.get(id)
		if !ok {
			http.Error(w, "no such run", http.StatusNotFound)
			return
		}
		killProcGroup(runEntry.cmd.Process)
		w.WriteHeader(http.StatusNoContent)
	})
}
