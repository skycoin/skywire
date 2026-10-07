// Package main cmd/skywire-mobile-core/main.go c4-vis-core
/*
skywire-mobile-core is the Skywire mobile core as a C library, for the iOS
app: `make ios-core` builds it with -buildmode=c-archive for the device and
Simulator slices and packages them as ios/Frameworks/SkywireCore.xcframework.
iOS cannot exec a child process, so where Android runs cmd/skywire-mobile as
libskywire-mobile.so, iOS links this archive and calls pkg/mobilecore through
the eight functions below. They cover the lifecycle only; the app drives
everything else over the visor's local HTTP API, as Android does.

C API (the generated header, skywire_core.h, carries the prototypes):

	int32_t skywire_start(const char *config_path, const char *data_dir);
	int32_t skywire_stop(int32_t timeout_ms);
	int32_t skywire_state(void);
	char   *skywire_last_error(void);
	int32_t skywire_config_gen(const char *out_path, const char *options_json);
	int32_t skywire_set_tun_fd(int32_t fd);
	void    skywire_set_log_sink(skywire_log_fn cb);
	void    skywire_free(void *p);

Ownership: every char * this library returns is allocated with malloc and
belongs to the caller, who releases it with skywire_free. Strings passed in
are only read during the call. The log sink receives a line that is valid
only for the duration of the callback; copy it to keep it. The callback runs
on whichever thread logged, possibly several at once, and must not block.
*/
package main

/*
#include <stdint.h>
#include <stdlib.h>

// skywire_log_fn receives one log line: its level (0 panic, 1 fatal, 2 error,
// 3 warning, 4 info, 5 debug, 6 trace) and the text, valid only during the
// call.
typedef void (*skywire_log_fn)(int32_t level, const char *line);

static inline void skywire_call_log(skywire_log_fn fn, int32_t level, const char *line) {
	fn(level, line);
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/skycoin/skywire/pkg/mobilecore"
)

// Return codes of the int32_t functions: 0 is success, -1 a failure whose
// reason skywire_last_error returns.
const (
	codeOK   = 0
	codeFail = -1
)

// lastCallErr is the reason the last failing call gave, for skywire_last_error
// when the core's own last error is empty (a config_gen or set_tun_fd error,
// or a refusal before the core started).
var lastCallErr atomic.Pointer[string]

func fail(err error) C.int32_t {
	msg := err.Error()
	lastCallErr.Store(&msg)
	return codeFail
}

func succeed() C.int32_t {
	lastCallErr.Store(nil)
	return codeOK
}

// skywire_start starts the core with the config at config_path, running in
// data_dir (which becomes the process's working directory; may be NULL). It
// blocks until the visor's local API is up or the start failed: call it off
// the main thread.
//
//export skywire_start
func skywire_start(configPath, dataDir *C.char) C.int32_t {
	opts := mobilecore.Options{ConfigPath: C.GoString(configPath)}
	if dataDir != nil {
		opts.DataDir = C.GoString(dataDir)
	}
	if err := mobilecore.Start(context.Background(), opts); err != nil {
		return fail(err)
	}
	return succeed()
}

// skywire_stop stops the core, or aborts a start in progress, waiting up to
// timeout_ms for its listeners to close.
//
//export skywire_stop
func skywire_stop(timeoutMS C.int32_t) C.int32_t {
	if err := mobilecore.Stop(time.Duration(timeoutMS) * time.Millisecond); err != nil {
		return fail(err)
	}
	return succeed()
}

// skywire_state returns the core's state: 0 stopped, 1 starting, 2 running,
// 3 stopping, 4 failed.
//
//export skywire_state
func skywire_state() C.int32_t {
	return C.int32_t(mobilecore.CurrentState())
}

// skywire_last_error returns why the last call failed (or why the core
// failed), or NULL when nothing did. Free the result with skywire_free.
//
//export skywire_last_error
func skywire_last_error() *C.char {
	if p := lastCallErr.Load(); p != nil {
		return C.CString(*p)
	}
	if msg := mobilecore.LastError(); msg != "" {
		return C.CString(msg)
	}
	return nil
}

// skywire_config_gen writes a config to out_path with `config gen`, run in
// this process. options_json (may be NULL or "{}") overrides fields of the
// phone's defaults — the argv Android generates with — by their JSON names
// in mobilecore.GenOptions, e.g. {"bin_path": "/…/bin"}.
//
//export skywire_config_gen
func skywire_config_gen(outPath, optionsJSON *C.char) C.int32_t {
	o := mobilecore.PhoneGenOptions("", "")
	if optionsJSON != nil {
		if raw := C.GoString(optionsJSON); raw != "" {
			if err := json.Unmarshal([]byte(raw), &o); err != nil {
				return fail(err)
			}
		}
	}
	o.OutPath = C.GoString(outPath)
	if err := mobilecore.GenConfig(o); err != nil {
		return fail(err)
	}
	return succeed()
}

// skywire_set_tun_fd hands the core the packet-tunnel extension's utun
// descriptor. The extension keeps ownership of it.
//
//export skywire_set_tun_fd
func skywire_set_tun_fd(fd C.int32_t) C.int32_t {
	if err := mobilecore.SetTunFD(int(fd)); err != nil {
		return fail(err)
	}
	return succeed()
}

// skywire_set_log_sink installs cb as the log sink, replacing the previous
// one; NULL removes it. Can be called before skywire_start.
//
//export skywire_set_log_sink
func skywire_set_log_sink(cb C.skywire_log_fn) {
	if cb == nil {
		mobilecore.SetLogSink(nil)
		return
	}
	mobilecore.SetLogSink(func(level int32, line string) {
		cs := C.CString(line)
		C.skywire_call_log(cb, C.int32_t(level), cs)
		C.free(unsafe.Pointer(cs))
	})
}

// skywire_free releases memory this library returned.
//
//export skywire_free
func skywire_free(p unsafe.Pointer) {
	C.free(p)
}

// main is required by -buildmode=c-archive and never runs.
func main() {}
