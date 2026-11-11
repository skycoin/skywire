// Package visor pkg/visor/hypervisor_park.go c3-vis-core
//
// A hypervisor's change to a managed visor that is offline waits on the
// visor's dmsg-async inbox, labeled with the hypervisor port. The visor
// serves the recorded calls when it fetches them, as if the hypervisor had
// made them over its RPC connection.
package visor

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/0magnet/gobrpc/gobimpl"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgasync"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// hvParkAllowed lists the calls a hypervisor may leave for an offline visor:
// settings that are as right when the visor returns as they are now.
var hvParkAllowed = map[string]bool{
	"SetAutoStart": true, "SetAppKillswitch": true, "SetAppSecure": true,
	"SetAppAddress": true, "SetAppWhitelist": true, "SetAppNetworkInterface": true,
	"SetAppDNS": true, "SetAppPK": true, "SetAppArgs": true, "SetAppEnvBatch": true,
	"SetAppEnvFull": true, "SetAppLauncherMode": true, "DoCustomSetting": true,
	"StartApp": true, "StopApp": true, "RestartApp": true,
	"SetPublicAutoconnect": true, "SetMinHops": true, "SetSkyenv": true,
	"SetPersistentTransports": true, "SetRewardAddress": true, "SetSingleRewardAddress": true,
	"DeleteRewardAddress": true, "SetIsPublic": true, "SetRuntimeConfig": true,
}

func hvParkAllows(method string) bool { return hvParkAllowed[method] }

// hvParked is one change: the calls one hypervisor request made, in order.
type hvParked struct {
	ID     string   `json:"id"`
	Frames [][]byte `json:"frames"`
}

const (
	hvParkedSeenFile = "hypervisor-applied.json"
	// hvParkedSeenFor outlasts the longest a host keeps a message, so a host
	// that hands a change over twice gets it applied once.
	hvParkedSeenFor     = dmsgasync.MaxRecordLifetime + 24*time.Hour
	hvParkedCallTimeout = 2 * time.Minute
	// hvParkedStartWait bounds waiting for the visor to finish starting before
	// a change is served, since most calls need the app launcher.
	hvParkedStartWait = 5 * time.Minute
	// hvParkedHeader names the inbox host on a 202 for a change left there.
	hvParkedHeader = "X-Skywire-Parked"
)

// parkForVisor leaves frames for pk on its inbox, sealed to pk and signed
// with this visor's key.
func (v *Visor) parkForVisor(pk cipher.PubKey, frames [][]byte) (cipher.PubKey, error) {
	if v.dmsgC == nil || v.conf == nil {
		return cipher.PubKey{}, ErrDmsgAsyncOff
	}
	body, err := json.Marshal(hvParked{ID: dmsgasync.NewRef(), Frames: frames})
	if err != nil {
		return cipher.PubKey{}, err
	}
	env, err := skymail.Seal(body, v.conf.SK, pk)
	if err != nil {
		return cipher.PubKey{}, err
	}
	res, err := v.DmsgAsyncSend(visorapi.DmsgAsyncSendRequest{To: pk, Port: skyenv.DmsgHypervisorPort, Payload: env})
	if err != nil {
		return cipher.PubKey{}, err
	}
	return res.Host, nil
}

// takeHypervisorParked serves a change a hypervisor left here. Only a
// configured hypervisor's changes apply, each once, and only allowed calls.
func (v *Visor) takeHypervisorParked(m dmsgasync.Message) error {
	log := v.async.log
	body, signer, err := skymail.OpenSealed(m.Payload, v.conf.SK)
	if err != nil {
		return nil
	}
	if !slices.Contains(v.configuredHypervisors(), signer) {
		log.WithField("from", signer.Hex()).Warn("dmsg-async: dropped a change from a key that is not a hypervisor of this visor")
		return nil
	}
	var p hvParked
	if err := json.Unmarshal(body, &p); err != nil || p.ID == "" {
		return nil
	}
	if !v.waitStarted(hvParkedStartWait) {
		return errors.New("the visor is still starting")
	}
	if !v.hvParkedFirst(p.ID) {
		return nil
	}
	for _, f := range p.Frames {
		method, err := v.serveParkedCall(signer, f)
		l := log.WithField("hypervisor", signer.Hex()).WithField("call", method)
		if err != nil {
			l.WithError(err).Warn("dmsg-async: a change the hypervisor left did not apply")
			continue
		}
		l.Info("dmsg-async: applied a change the hypervisor left while this visor was offline")
	}
	return nil
}

// serveParkedCall serves one recorded request frame on a fresh RPC server, as
// the hypervisor's RPC connection would.
func (v *Visor) serveParkedCall(hv cipher.PubKey, frame []byte) (string, error) {
	var req gobimpl.Request
	if err := gob.NewDecoder(bytes.NewReader(frame)).Decode(&req); err != nil {
		return "", err
	}
	method := strings.TrimPrefix(req.ServiceMethod, visorapi.RPCPrefix+".")
	if !hvParkAllows(method) || req.ServiceMethod != visorapi.RPCPrefix+"."+method {
		return req.ServiceMethod, fmt.Errorf("not a call a hypervisor may leave")
	}
	rpcS, err := newRPCServer(v, "parked:"+hv.Hex())
	if err != nil {
		return method, err
	}
	srv, cli := net.Pipe()
	go rpcS.ServeConn(srv)
	defer cli.Close() //nolint:errcheck
	if err := cli.SetDeadline(time.Now().Add(hvParkedCallTimeout)); err != nil {
		return method, err
	}
	werr := make(chan error, 1)
	go func() {
		_, err := cli.Write(frame)
		werr <- err
	}()
	dec := gob.NewDecoder(cli)
	var resp gobimpl.Response
	if err := dec.Decode(&resp); err != nil {
		return method, err
	}
	_ = dec.DecodeValue(reflect.Value{}) //nolint:errcheck
	if err := <-werr; err != nil {
		return method, err
	}
	if resp.Error != "" {
		return method, errors.New(resp.Error)
	}
	return method, nil
}

var hvParkedSeenMu sync.Mutex

// hvParkedFirst reports whether change id has not been applied before, and
// remembers it.
func (v *Visor) hvParkedFirst(id string) bool {
	hvParkedSeenMu.Lock()
	defer hvParkedSeenMu.Unlock()
	dir := asyncDir(v.conf)
	path := filepath.Join(dir, hvParkedSeenFile)
	seen := map[string]time.Time{}
	if raw, err := os.ReadFile(path); err == nil { //nolint:gosec // our own file
		_ = json.Unmarshal(raw, &seen) //nolint:errcheck
	}
	if _, ok := seen[id]; ok {
		return false
	}
	now := time.Now()
	for k, at := range seen {
		if now.Sub(at) > hvParkedSeenFor {
			delete(seen, k)
		}
	}
	seen[id] = now
	raw, err := json.Marshal(seen)
	if err == nil && os.MkdirAll(dir, 0o700) == nil && os.WriteFile(path+".tmp", raw, 0o600) == nil {
		_ = os.Rename(path+".tmp", path) //nolint:errcheck
	}
	return true
}

// parkOrRefuse runs a request for a managed visor that is offline against a
// recording client. When it succeeds the calls it made are left on the
// visor's inbox and it answers 202 with the handler's body. A request that
// needs the visor live is refused as before.
func (hv *Hypervisor) parkOrRefuse(w http.ResponseWriter, r *http.Request, rv *httpCtx, hFunc handlerFunc) {
	pk := rv.Addr.PK
	tw := newTimeoutResponseWriter()
	hFunc(tw, r, rv)
	frames := rv.rec.Frames()
	switch {
	case tw.statusCode == http.StatusBadRequest:
		tw.copyTo(w)
		return
	case tw.statusCode/100 != 2 || len(frames) == 0:
		httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
			fmt.Errorf("visor '%s' is currently disconnected and this cannot wait for it", pk))
		return
	}
	host, err := hv.visor.parkForVisor(pk, frames)
	if err != nil {
		httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
			fmt.Errorf("visor '%s' is currently disconnected, and the change could not be left on its inbox: %w", pk, err))
		return
	}
	tw.header.Set(hvParkedHeader, host.Hex())
	tw.statusCode = http.StatusAccepted
	tw.copyTo(w)
}

// waitStarted waits up to d for the visor to finish starting.
func (v *Visor) waitStarted(d time.Duration) bool {
	ch := v.startupComplete
	if ch == nil {
		return true
	}
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}
