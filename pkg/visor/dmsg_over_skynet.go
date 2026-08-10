// Package visor pkg/visor/dmsg_over_skynet.go c2-vis-net
//
// "dmsg over skynet transports": serve a .dmsg fetch by reaching the peer's
// :80 over a skynet transport via the VStreamMux relay (direct → 1-hop relay,
// route ID 0, PK-addressed, NO route-finder), with dmsg-servers as the only
// fallback. dmsg is a relay layer — this path never uses a route.
package visor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// dmsgOverSkynet tries to serve a DmsgHTTP request over the visor-relay instead
// of a dmsg-server session. Returns (resp, true) on success; (nil, false) to
// fall back to the dmsg-server path. Reaching the peer's :80 over skynet works
// because a visor mirrors :80 over both dmsg and its skynet forwarding server.
func (v *Visor) dmsgOverSkynet(req DmsgHTTPRequest) (*DmsgHTTPResponse, bool) {
	if v.router == nil || v.tpM == nil || v.skynetFwdMux == nil {
		return nil, false
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, false
	}
	var pk cipher.PubKey
	if err := pk.Set(u.Hostname()); err != nil {
		return nil, false
	}
	port := uint16(80)
	if ps := u.Port(); ps != "" {
		if p, perr := strconv.Atoi(ps); perr == nil {
			port = uint16(p) //nolint:gosec
		}
	}

	dialer := &routerSkynetDialer{
		router:       v.router,
		localPK:      v.conf.PK,
		log:          v.log,
		tpM:          v.tpM,
		skynetMuxPtr: &v.skynetFwdMux,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := dialer.dialDirectOrRelay(ctx, pk, port)
	if err != nil {
		return nil, false // no direct/relay path to the peer — use dmsg-servers
	}
	defer conn.Close() //nolint:errcheck,gosec

	method := req.Method
	if method == "" {
		method = "GET"
	}
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	hr, err := http.NewRequestWithContext(ctx, method, u.RequestURI(), body)
	if err != nil {
		return nil, false
	}
	hr.Host = fmt.Sprintf("%s:%d", pk.Hex(), port)
	for k, val := range req.Header {
		if strings.EqualFold(k, "Host") {
			hr.Host = val
			continue
		}
		hr.Header.Set(k, val)
	}
	if err := hr.Write(conn); err != nil {
		return nil, false
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), hr)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close() //nolint:errcheck
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	out := &DmsgHTTPResponse{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Header:     make(map[string]string, len(resp.Header)),
		Body:       rb,
	}
	for k, vv := range resp.Header {
		if len(vv) > 0 {
			out.Header[k] = vv[0]
		}
	}
	v.log.WithField("remote", pk.String()).WithField("port", port).
		Debug("dmsg-over-skynet: served .dmsg fetch over VStreamMux relay (no route, no dmsg-server)")
	return out, true
}
