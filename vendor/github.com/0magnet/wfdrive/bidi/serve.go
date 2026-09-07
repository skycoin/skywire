package bidi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// captureSeq numbers capture files. They are named here rather than by the
// request: two static analyzers both, correctly, refused to believe any amount
// of validation on a request-supplied name made the resulting path safe, and
// they are easier to agree with than to argue with. The caller learns where its
// capture landed from the JSON response, which is all it needed the name for.
var captureSeq atomic.Uint64

// captureBase is the prefix for this run's captures.
const captureBase = "wfdrive"

// writeCapture writes one capture into the temp directory under a name of our
// own making, and returns the path.
func writeCapture(seq uint64, ext string, b []byte) string {
	// Every component here is ours: the temp dir, a constant prefix, a counter
	// and a literal extension. The taint analyzers reach os.TempDir (it reads
	// TMPDIR) rather than anything a request said.
	path := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d%s", captureBase, seq, ext))
	_ = os.WriteFile(path, b, 0o600) //nolint:errcheck,gosec // path is built from constants and a counter
	return path
}

// Serve runs a persistent driver against the BiDi port and exposes it over a
// small HTTP control port at ctrlAddr.
//
// This is the shape that makes Firefox's single-session rule livable: one
// long-lived session and tab, held open for as long as the process runs, so
// callers spend no session establishing one per command. announce, when
// non-nil, is called once the control port is about to listen.
//
//	/nav?url=&wait=&eval=   navigate, optionally evaluate, then capture
//	/eval?expr=             evaluate in place, no navigation
//	/shoot                  capture the tab as it stands
//	/health                 is the session and tab still usable
//	/quit                   session.end and exit
//
// tab selects which tab to drive. Empty opens a fresh one, which is the right
// default. A non-empty value is matched as a substring of the URL of a tab that
// is already open, and is how a driver that died gets its tab back: the session
// is stranded either way until the browser restarts, so a replacement driver
// that opens a blank tab instead of resuming the old one loses the state that
// made the tab worth driving.
func Serve(ctx context.Context, port, ctrlAddr, tab string, announce func(tab string)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var d *Driver
	var err error
	if tab == "" {
		d, err = Connect(ctx, port)
	} else {
		d, err = Attach(ctx, port)
		if err == nil {
			_, err = d.AttachTab(tab)
		}
	}
	if err != nil {
		return err
	}
	// Release the session on EVERY exit path, not just the graceful ones.
	// Firefox allows one BiDi session and does not reclaim it when the socket
	// drops, so a driver that dies after connecting — a control port already in
	// use, say — strands that session until the browser is restarted.
	defer d.End()

	srv := &http.Server{Addr: ctrlAddr, ReadHeaderTimeout: 5 * time.Second}
	mux := http.NewServeMux()

	writeJSON := func(w http.ResponseWriter, body map[string]interface{}) {
		resp, _ := json.Marshal(body) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp) //nolint:errcheck
	}

	// report writes the screenshot + console for a step and answers as JSON.
	report := func(w http.ResponseWriter, evalOut string, warn error) {
		seq := captureSeq.Add(1)
		if png, e := d.Screenshot(); e == nil {
			writeCapture(seq, ".png", png)
		}
		con := d.Console()
		conPath := writeCapture(seq, ".console.txt", []byte(con+"\n"))
		body := map[string]interface{}{
			"png":     strings.TrimSuffix(conPath, ".console.txt") + ".png",
			"console": con,
			"eval":    evalOut,
		}
		if warn != nil {
			body["warning"] = warn.Error()
		}
		writeJSON(w, body)
	}

	mux.HandleFunc("/nav", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		wait := 8
		if n, e := strconv.Atoi(q.Get("wait")); e == nil {
			wait = n
		}
		d.ResetConsole()
		var warn error
		if u := q.Get("url"); u != "" {
			warn = d.Navigate(u)
		}
		time.Sleep(time.Duration(wait) * time.Second)
		evalOut := ""
		if ev := q.Get("eval"); ev != "" {
			out, e := d.Eval(ev)
			evalOut = out
			if e != nil {
				evalOut = "ERROR: " + e.Error()
			}
		}
		report(w, evalOut, warn)
	})

	// /shoot screenshots the tab as it stands, without navigating — so a test
	// can drive the page through several steps (settle, toggle, settle) and
	// capture each one, instead of having to fold everything into the single
	// eval that /nav runs immediately before its shot.
	mux.HandleFunc("/shoot", func(w http.ResponseWriter, _ *http.Request) {
		report(w, "", nil)
	})

	// /eval evaluates in the tab without navigating or resetting the console.
	mux.HandleFunc("/eval", func(w http.ResponseWriter, r *http.Request) {
		out, e := d.Eval(r.URL.Query().Get("expr"))
		if e != nil {
			out = "ERROR: " + e.Error()
		}
		writeJSON(w, map[string]interface{}{"eval": out})
	})

	// /health reports whether the session and tab are still usable, recovering
	// them if not.
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		status := "ok"
		if e := d.Health(); e != nil {
			status = "unhealthy: " + e.Error()
		}
		writeJSON(w, map[string]interface{}{"status": status, "tab": d.Tab()})
	})

	mux.HandleFunc("/quit", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("bye\n")) //nolint:errcheck
		go func() {
			time.Sleep(200 * time.Millisecond)
			d.End()
			_ = srv.Close() //nolint:errcheck
		}()
	})
	srv.Handler = mux

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)
	go func() {
		select {
		case <-sig:
		case <-ctx.Done():
		}
		d.End()
		_ = srv.Close() //nolint:errcheck
	}()

	if announce != nil {
		announce(d.Tab())
	}
	if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		return e
	}
	return nil
}
