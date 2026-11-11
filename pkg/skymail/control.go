package skymail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TokenFile holds the token a control request over TCP must carry in
// TokenHeader. The app writes it beside the mail when it binds a TCP
// address, so only who can read the mail can use it.
const (
	TokenFile   = "control-token"
	TokenHeader = "X-Skymail-Token" //nolint:gosec // a header name, not a credential
)

// maxControlBody bounds a request to the control surface: a message at
// the largest size a mailbox takes, base64'd attachments included.
const maxControlBody = 64 << 20

type controlError struct {
	Error string `json:"error"`
}

// SendReply is the control surface's answer to a send. Send can fail with
// a result, which says why each recipient was refused.
type SendReply struct {
	Result *SendResult `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

type messageRef struct {
	Folder string `json:"folder"`
	ID     string `json:"id"`
}

// Handler is the control surface of s, which the visor's mail API calls.
// token, when set, must arrive in TokenHeader.
func (s *Service) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, s.Status())
	})
	mux.HandleFunc("GET /list", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.mb.List(r.URL.Query().Get("folder"))
		reply(w, list, err)
	})
	mux.HandleFunc("GET /read", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		raw, err := s.mb.Raw(q.Get("folder"), q.Get("id"))
		if err != nil {
			fail(w, err)
			return
		}
		m, err := Render(raw)
		if err == nil {
			_ = s.mb.MarkSeen(q.Get("folder"), q.Get("id")) //nolint:errcheck // shown is what matters
		}
		reply(w, m, err)
	})
	mux.HandleFunc("GET /raw", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		raw, err := s.mb.Raw(q.Get("folder"), q.Get("id"))
		reply(w, raw, err)
	})
	mux.HandleFunc("GET /attachment", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		n, err := strconv.Atoi(q.Get("n"))
		if err != nil {
			http.Error(w, `{"error":"bad attachment number"}`, http.StatusBadRequest)
			return
		}
		a, err := s.mb.Attachment(q.Get("folder"), q.Get("id"), n)
		reply(w, a, err)
	})
	mux.HandleFunc("POST /send", func(w http.ResponseWriter, r *http.Request) {
		var out Outgoing
		if !readJSON(w, r, &out) {
			return
		}
		res, err := s.mb.Send(r.Context(), out)
		rep := SendReply{Result: res}
		if err != nil {
			rep.Error = err.Error()
		}
		writeJSON(w, rep)
	})
	mux.HandleFunc("POST /delete", func(w http.ResponseWriter, r *http.Request) {
		var m messageRef
		if readJSON(w, r, &m) {
			reply(w, nil, s.mb.Delete(m.Folder, m.ID))
		}
	})
	mux.HandleFunc("POST /retry", func(w http.ResponseWriter, r *http.Request) {
		var m messageRef
		if readJSON(w, r, &m) {
			reply(w, nil, s.mb.Retry(m.ID))
		}
	})
	mux.HandleFunc("POST /whitelist", func(w http.ResponseWriter, r *http.Request) {
		var pks []cipher.PubKey
		if readJSON(w, r, &pks) {
			reply(w, nil, s.mb.SetWhitelist(pks))
		}
	})
	mux.HandleFunc("POST /reload", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, nil, s.Reload())
	})
	if token == "" {
		return mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(TokenHeader) != token {
			http.Error(w, `{"error":"bad control token"}`, http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxControlBody)).Decode(v); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(controlError{Error: err.Error()}) //nolint:errcheck
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck // the client sees a short body
}

func fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, ErrNotFound) {
		code = http.StatusNotFound
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(controlError{Error: err.Error()}) //nolint:errcheck
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, v)
}

// Control calls a mailbox's control surface. Do sends one request and is
// where the caller decides how to reach the app: its in-process handler,
// or its TCP address with the token.
type Control struct {
	Do func(*http.Request) (*http.Response, error)
}

func (c Control) call(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	target := path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://skymail"+target, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxControlBody))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e controlError
		if json.Unmarshal(raw, &e) != nil || e.Error == "" {
			e.Error = fmt.Sprintf("mailbox answered %s", resp.Status)
		}
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w (%s)", ErrNotFound, e.Error)
		}
		return errors.New(e.Error)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// controlTimeout bounds a call to the mailbox. A send waits on the network,
// and the visor's hypervisor handler lets that outlast its write timeout.
const controlTimeout = 2 * time.Minute

func (c Control) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), controlTimeout)
}

func ref(folder, id string) url.Values {
	return url.Values{"folder": {folder}, "id": {id}}
}

// Status reports the mailbox's state.
func (c Control) Status() (Status, error) {
	ctx, cancel := c.ctx()
	defer cancel()
	var st Status
	err := c.call(ctx, http.MethodGet, "/status", nil, nil, &st)
	return st, err
}

// List lists a folder, newest first.
func (c Control) List(folder string) ([]Summary, error) {
	ctx, cancel := c.ctx()
	defer cancel()
	var list []Summary
	err := c.call(ctx, http.MethodGet, "/list", url.Values{"folder": {folder}}, nil, &list)
	return list, err
}

// Read renders one message and marks it read.
func (c Control) Read(folder, id string) (*Rendered, error) {
	ctx, cancel := c.ctx()
	defer cancel()
	var m Rendered
	if err := c.call(ctx, http.MethodGet, "/read", ref(folder, id), nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Raw returns one message as stored.
func (c Control) Raw(folder, id string) ([]byte, error) {
	ctx, cancel := c.ctx()
	defer cancel()
	var raw []byte
	err := c.call(ctx, http.MethodGet, "/raw", ref(folder, id), nil, &raw)
	return raw, err
}

// Attachment decodes attachment n of a message.
func (c Control) Attachment(folder, id string, n int) (*AttachmentData, error) {
	ctx, cancel := c.ctx()
	defer cancel()
	q := ref(folder, id)
	q.Set("n", strconv.Itoa(n))
	var a AttachmentData
	if err := c.call(ctx, http.MethodGet, "/attachment", q, nil, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Send delivers out, as Mailbox.Send does.
func (c Control) Send(out Outgoing) (*SendResult, error) {
	ctx, cancel := c.ctx()
	defer cancel()
	var rep SendReply
	if err := c.call(ctx, http.MethodPost, "/send", nil, out, &rep); err != nil {
		return nil, err
	}
	if rep.Error != "" {
		return rep.Result, errors.New(rep.Error)
	}
	return rep.Result, nil
}

// Delete removes one message. In the Outbox that cancels it.
func (c Control) Delete(folder, id string) error {
	ctx, cancel := c.ctx()
	defer cancel()
	return c.call(ctx, http.MethodPost, "/delete", nil, messageRef{Folder: folder, ID: id}, nil)
}

// Retry makes an Outbox message due now, or every one when id is empty.
func (c Control) Retry(id string) error {
	ctx, cancel := c.ctx()
	defer cancel()
	return c.call(ctx, http.MethodPost, "/retry", nil, messageRef{ID: id}, nil)
}

// SetWhitelist replaces the PKs allowed to deliver.
func (c Control) SetWhitelist(pks []cipher.PubKey) error {
	ctx, cancel := c.ctx()
	defer cancel()
	if pks == nil {
		pks = []cipher.PubKey{}
	}
	return c.call(ctx, http.MethodPost, "/whitelist", nil, pks, nil)
}

// Reload applies SettingsFile as it now stands.
func (c Control) Reload() error {
	ctx, cancel := c.ctx()
	defer cancel()
	return c.call(ctx, http.MethodPost, "/reload", nil, nil, nil)
}
