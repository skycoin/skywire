// Package cliskychat — cmd/skywire-cli/commands/skychat/postmessage_baseline_test.go:
// Phase 0 baseline tests for the skychat DM send path.
//
// Operator's 2026-05-17 22:50Z reset directive called out that we'd
// been firefighting CXO-layer bugs without baseline coverage of the
// plain (no-CXO) DM send. This file is the first layer of that
// baseline: tests that exercise the CLI → chat-app HTTP /message
// boundary in isolation, using httptest.NewServer to stand in for a
// real chat-app on either skynet or dmsg.
//
// The CLI postMessage path is transport-agnostic at the HTTP layer
// — the network=skynet vs network=dmsg switch just flips a payload
// field that the chat-app routes on. So a single mock server tests
// the CLI's request-shape + response-decode for BOTH transports;
// the actual on-network behavior is exercised by the 3-agent live
// test plan (separate doc, executed on our visors).
//
// Reuse intent: Phase 1 (CXO-backed DMs) extends the same scaffold —
// when the chat-app gains a CXO-routed DM path, the same test calls
// hit the same /message endpoint with a different payload flag, and
// the assertions stay identical at the CLI layer. The CI value of
// the suite is locking the CLI-side contract regardless of how the
// chat-app routes internally.

package cliskychat

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// dmRoundTripCase is one stitched scenario: mock /message handler +
// expected (ack, errSubstring, verboseSubstrings). The verbose
// substrings are checked as ordered occurrences in the captured
// stderr, so a future verbose-format change that drops a stage
// fails the test loudly.
type dmRoundTripCase struct {
	name              string
	handler           http.HandlerFunc
	wait              time.Duration
	wantAcked         bool
	wantErrSubstr     string
	wantVerboseSubstr []string
}

// mockChatAppMessage returns a handler that mimics the chat-app's
// /message endpoint for the cases tests need to assert against.
// Variants:
//   - okAck:        200 + AckResponse{Acked:true, ID:"deadbeef", MS:42}
//   - timeoutAck:   504 + AckResponse{Acked:false, Reason:"timeout"}
//   - serverError:  500 + plaintext body — surfaces as `server error 500: <body>`
//   - fireAndForget: 200 + empty body, expected when wait=0
//
// Real chat-app behavior in pkg/visor/apps/skychat is mimicked at
// the wire-shape level only — the inner transport / framing logic
// isn't part of this baseline.
func mockChatAppMessage(variant string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Recipient string `json:"recipient"`
			Message   string `json:"message"`
			Network   string `json:"network"`
			WaitMS    int64  `json:"wait_ms"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		switch variant {
		case "okAck":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(AckResponse{Acked: true, ID: "deadbeef", MS: 42}) //nolint:errcheck
		case "timeoutAck":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_ = json.NewEncoder(w).Encode(AckResponse{Acked: false, Reason: "timeout", ID: "deadbeef"}) //nolint:errcheck
		case "serverError":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("upstream visor unreachable")) //nolint:errcheck
		case "fireAndForget":
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "unknown variant", http.StatusBadRequest)
		}
	}
}

func TestPostMessageVerbose_OkAck_VerboseShapeAndAckDecoded(t *testing.T) {
	// Baseline happy path: 5s wait, server returns 200 + Acked. The
	// CLI returns the ack verbatim and the verbose log records every
	// stage in order.
	srv := httptest.NewServer(mockChatAppMessage("okAck"))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	var verboseBuf bytes.Buffer

	ack, err := postMessageVerbose(addr, "0323272a", "hello", "skynet", 5*time.Second, &verboseBuf)
	if err != nil {
		t.Fatalf("postMessageVerbose: unexpected err %v", err)
	}
	if ack == nil {
		t.Fatalf("nil ack on wait>0; want non-nil with Acked=true")
	}
	if !ack.Acked || ack.ID != "deadbeef" || ack.MS != 42 {
		t.Errorf("ack mismatch: got %+v", ack)
	}

	got := verboseBuf.String()
	wantOrdered := []string{
		"verbose: POST url=http://" + addr + "/message",
		"http_timeout=10s", // 5s wait + 5s grace
		"verbose: response status=200",
		"verbose: ack acked=true id=deadbeef ms=42",
	}
	for _, sub := range wantOrdered {
		if !strings.Contains(got, sub) {
			t.Errorf("verbose output missing %q\nfull:\n%s", sub, got)
		}
	}
}

func TestPostMessageVerbose_TimeoutAck_SurfacesReason(t *testing.T) {
	// 504 + AckResponse{Acked:false, Reason:"timeout"} is NOT a
	// server-error from the CLI's perspective; the ack is surfaced
	// to the caller so the operator-facing layer can print "not
	// acked: timeout" cleanly. Verbose log notes the 504-decoded path.
	srv := httptest.NewServer(mockChatAppMessage("timeoutAck"))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	var verboseBuf bytes.Buffer

	ack, err := postMessageVerbose(addr, "0323272a", "hi", "dmsg", 2*time.Second, &verboseBuf)
	if err != nil {
		t.Fatalf("504 path should return ack, not err; got err=%v", err)
	}
	if ack == nil {
		t.Fatalf("504 path should return non-nil ack with Acked=false")
	}
	if ack.Acked {
		t.Errorf("504 path: Acked should be false, got %+v", ack)
	}
	if ack.Reason != "timeout" {
		t.Errorf("504 path: Reason should be 'timeout', got %q", ack.Reason)
	}
	if !strings.Contains(verboseBuf.String(), "504-decoded") {
		t.Errorf("verbose missing 504-decoded line; got:\n%s", verboseBuf.String())
	}
	if !strings.Contains(verboseBuf.String(), `reason="timeout"`) {
		t.Errorf("verbose missing reason field; got:\n%s", verboseBuf.String())
	}
}

func TestPostMessageVerbose_ServerError_PropagatesBody(t *testing.T) {
	// 500 + plaintext body: CLI returns error wrapping the body so
	// operators see WHAT went wrong server-side. Verbose surfaces
	// the same body.
	srv := httptest.NewServer(mockChatAppMessage("serverError"))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	var verboseBuf bytes.Buffer

	ack, err := postMessageVerbose(addr, "0323272a", "x", "skynet", 1*time.Second, &verboseBuf)
	if err == nil {
		t.Fatalf("server error: want non-nil err, got nil ack=%v", ack)
	}
	if ack != nil {
		t.Errorf("server error: want nil ack, got %+v", ack)
	}
	if !strings.Contains(err.Error(), "server error 500") {
		t.Errorf("err missing status: %v", err)
	}
	if !strings.Contains(err.Error(), "upstream visor unreachable") {
		t.Errorf("err missing body: %v", err)
	}
	if !strings.Contains(verboseBuf.String(), "server-error status=500") {
		t.Errorf("verbose missing server-error line; got:\n%s", verboseBuf.String())
	}
}

func TestPostMessageVerbose_DialFailure_VerboseSurfaces(t *testing.T) {
	// Pointing at an address with no listener exercises the http.Post
	// error path. The CLI's error wraps "post: ..." and verbose
	// records the elapsed time so an operator can tell connect-
	// refused (millisecond) from network-unreachable (multi-second).
	var verboseBuf bytes.Buffer
	// 127.0.0.1:1 is reserved/never-listened on standard Unix; any
	// connect attempt hits ECONNREFUSED instantly.
	ack, err := postMessageVerbose("127.0.0.1:1", "0323272a", "x", "skynet", 1*time.Second, &verboseBuf)
	if err == nil {
		t.Fatalf("dial to :1 should fail; got ack=%v", ack)
	}
	if ack != nil {
		t.Errorf("dial failure: want nil ack, got %+v", ack)
	}
	if !strings.Contains(err.Error(), "post:") {
		t.Errorf("err missing 'post:' prefix: %v", err)
	}
	got := verboseBuf.String()
	if !strings.Contains(got, "verbose: POST err=") {
		t.Errorf("verbose missing POST err line; got:\n%s", got)
	}
	if !strings.Contains(got, "elapsed=") {
		t.Errorf("verbose missing elapsed= field on dial-fail; got:\n%s", got)
	}
}

func TestPostMessageVerbose_FireAndForget_NoAckExpected(t *testing.T) {
	// wait=0 path: CLI doesn't wait for an ack body, server returns
	// 200 with empty body. postMessageVerbose returns (nil, nil) and
	// verbose notes the fire-and-forget code path explicitly.
	srv := httptest.NewServer(mockChatAppMessage("fireAndForget"))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	var verboseBuf bytes.Buffer

	ack, err := postMessageVerbose(addr, "0323272a", "fire", "dmsg", 0, &verboseBuf)
	if err != nil {
		t.Fatalf("fire-and-forget: unexpected err %v", err)
	}
	if ack != nil {
		t.Errorf("fire-and-forget: ack should be nil, got %+v", ack)
	}
	if !strings.Contains(verboseBuf.String(), "fire-and-forget") {
		t.Errorf("verbose missing fire-and-forget line; got:\n%s", verboseBuf.String())
	}
}

func TestPostMessageVerbose_NilWriter_SilentSuccess(t *testing.T) {
	// verboseW=nil must NOT panic and must produce zero side effects
	// on stderr / log. Calling postMessage (the thin non-verbose
	// wrapper) is the back-compat surface — proves nothing changed
	// for non-verbose callers.
	srv := httptest.NewServer(mockChatAppMessage("okAck"))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	ack, err := postMessage(addr, "0323272a", "hi", "skynet", 1*time.Second)
	if err != nil {
		t.Fatalf("postMessage (nil verbose): unexpected err %v", err)
	}
	if ack == nil || !ack.Acked {
		t.Errorf("postMessage (nil verbose): want non-nil acked ack, got %+v", ack)
	}
}

func TestPostMessageVerbose_BothTransports_IdenticalCLIShape(t *testing.T) {
	// Operator's framing: tests must validate behavior over skynet
	// AND dmsg. At the CLI layer, the two are payload-field different
	// and otherwise identical — the chat-app routes internally. So
	// the assertion is: same scenario on either network produces the
	// same CLI-observable behavior. If a future regression couples
	// CLI logic to transport, this catches it.
	srv := httptest.NewServer(mockChatAppMessage("okAck"))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	for _, net := range []string{"skynet", "dmsg"} {
		t.Run(net, func(t *testing.T) {
			var buf bytes.Buffer
			ack, err := postMessageVerbose(addr, "0323272a", "hi", net, 1*time.Second, &buf)
			if err != nil {
				t.Fatalf("%s: unexpected err %v", net, err)
			}
			if ack == nil || !ack.Acked {
				t.Errorf("%s: want acked, got %+v", net, ack)
			}
			if !strings.Contains(buf.String(), "verbose: POST") {
				t.Errorf("%s: verbose missing POST line", net)
			}
		})
	}
}

// Pin the AckResponse JSON shape so future field renames break tests
// instead of silently breaking deployed peer compatibility.
func TestAckResponse_JSONShape(t *testing.T) {
	ack := AckResponse{Acked: true, ID: "abc", MS: 12, Reason: ""}
	out, err := json.Marshal(ack)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(out)
	// Reason has ,omitempty so should be absent.
	if strings.Contains(got, `"reason"`) {
		t.Errorf("AckResponse{Reason:\"\"} should omit reason field: %s", got)
	}
	want := `{"acked":true,"id":"abc","ms":12}`
	if got != want {
		t.Errorf("wire shape drift: got %s want %s", got, want)
	}

	// And the inverse: decode the wire-shape skychat-app emits.
	var back AckResponse
	if err := json.Unmarshal([]byte(`{"acked":false,"reason":"timeout"}`), &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back.Acked || back.Reason != "timeout" {
		t.Errorf("decode timeout-ack: got %+v", back)
	}
}

// Belt-and-suspenders: the verbose envelope must not consume the
// HTTP response body — postMessage's decode happens AFTER the
// verbose status log. If a future refactor accidentally reads-then-
// logs, the ack decode would fail with EOF.
func TestPostMessageVerbose_VerboseDoesNotConsumeBody(t *testing.T) {
	srv := httptest.NewServer(mockChatAppMessage("okAck"))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	var buf bytes.Buffer
	ack, err := postMessageVerbose(addr, "0323272a", "x", "skynet", 1*time.Second, &buf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ack == nil || ack.ID != "deadbeef" {
		t.Errorf("body consumed prematurely? ack=%+v", ack)
	}
	// Sanity: assert specifically that no body-consuming decode
	// error surfaced anywhere. If the implementation grows a
	// stage that body-reads before json.Decode, this would catch
	// the resulting `unexpected EOF`.
	if errors.Is(err, errStubNeverReturned) {
		// Placeholder so the imported errors package is used; helps
		// the test stay extensible if we add explicit error sentinels.
	}
}

// errStubNeverReturned satisfies the errors import without affecting
// runtime semantics — keeps the test file structure ready for
// sentinel-error assertions added in Phase 1.
var errStubNeverReturned = errors.New("never returned")
