// Package skysocks pkg/skysocks/rangesplit_https.go — HTTPS (TLS-terminating)
// range-splitting.
//
// The plaintext splitter (rangesplit.go) cannot see the GET inside a :443 CONNECT.
// This file terminates that TLS at the proxy — presenting the browser a leaf minted
// on demand by a local UNCONSTRAINED root — reads the plaintext GET, and fetches the
// body as N concurrent byte ranges over separate exit streams, each wrapped in a
// fresh TLS client connection to the REAL origin (verified against the system roots,
// so the proxy never downgrades origin security). The reassembled 200 is written
// back to the browser over the terminated TLS. Anything not splittable becomes a
// transparent plaintext MITM splice between the browser TLS conn and one origin TLS
// conn.
//
// This is a deliberate security decision and therefore OFF by default:
//   - The minting root can forge a leaf for ANY host, so it is generated explicitly
//     (skynetca.CAOptions.Unconstrained) and the operator must import it into the
//     browser's trust store for the terminated TLS to be accepted.
//   - The origin side is verified normally; a MITM here interposes on the mesh path
//     the exit already sees, it does not weaken what the origin proves.
package skysocks

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"time"

	"github.com/skycoin/skywire/pkg/skynetca"
)

// mitmCACommonName names the unconstrained root in the browser's cert manager so an
// operator can find (and later remove) exactly what they trusted.
const mitmCACommonName = "Skywire Range-Split MITM CA"

// isPort443 reports whether target ("host:port") is HTTPS.
func isPort443(target string) bool {
	_, port, err := net.SplitHostPort(target)
	if err != nil {
		return false
	}
	return port == "443"
}

// loadOrCreateMITMCA loads the unconstrained MITM root from dir, generating and
// persisting one on first use. dir/ca.crt is the cert to import into the browser;
// dir/ca.key is its private key (0600). Returns the cert, a leaf minter that will
// sign for any host, and the cert itself for export.
func loadOrCreateMITMCA(dir string) (*x509.Certificate, skynetca.LeafMinter, error) {
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")

	cert, key, err := skynetca.LoadCA(certPath, keyPath)
	if err != nil {
		if !os.IsNotExist(errCause(err)) {
			// A present-but-unreadable CA is a real error — do not silently mint a
			// second root over it.
			return nil, nil, fmt.Errorf("load MITM CA: %w", err)
		}
		cert, key, err = skynetca.GenerateCA(skynetca.CAOptions{
			CommonName:    mitmCACommonName,
			Unconstrained: true,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("generate MITM CA: %w", err)
		}
		if err := skynetca.SaveCA(cert, key, certPath, keyPath); err != nil {
			return nil, nil, fmt.Errorf("save MITM CA: %w", err)
		}
	}
	return cert, newUnconstrainedMinter(cert, key), nil
}

// newUnconstrainedMinter builds a minter that permits any host. The empty suffix
// matches every non-empty hostname (see CachedMinter.permitted), which is exactly
// what an unconstrained MITM root is for.
func newUnconstrainedMinter(cert *x509.Certificate, key *ecdsa.PrivateKey) skynetca.LeafMinter {
	return skynetca.NewMinter(cert, key, skynetca.LeafOptions{PermittedSuffixes: []string{""}})
}

// errCause unwraps to the innermost error so os.IsNotExist sees the syscall error
// through skynetca's fmt.Errorf wrapping.
func errCause(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok || u.Unwrap() == nil {
			return err
		}
		err = u.Unwrap()
	}
}

// mitmCACertPEM returns the PEM encoding of the MITM root, for the operator to
// import into a browser. ok is false when HTTPS range-splitting is not configured.
func (c *Client) mitmCACertPEM() ([]byte, bool) {
	if c.rs.caCert == nil {
		return nil, false
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.rs.caCert.Raw}), true
}

// originTLSConfig is the client-side TLS config used to reach the REAL origin. It
// verifies against originRoots (nil = system roots), so the proxy does not weaken
// what the origin must prove; tests inject the httptest server's cert here.
func (c *Client) originTLSConfig(host string) *tls.Config {
	return &tls.Config{
		ServerName: host,
		RootCAs:    c.rs.originRoots,
		MinVersion: tls.VersionTLS12,
	}
}

// serveHTTPSRangeSplit takes ownership of a freshly-CONNECTed browser conn and the
// exit stream (already CONNECTing to host:443 on the browser's behalf). It relays the
// exit's SOCKS5 reply, terminates the browser's TLS with a minted leaf, and — when the
// first request is a splittable GET against a range-capable origin — fetches the body
// as N concurrent ranges over fresh TLS-to-origin streams, reassembling it into one
// 200. Non-splittable traffic becomes a plaintext MITM splice. It always closes both
// ends before returning.
func (c *Client) serveHTTPSRangeSplit(conn, stream net.Conn, target string) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}

	// 1. Read the exit's SOCKS5 reply (stream is now a raw pipe to host:443) and
	//    relay it to the browser so it proceeds to send its TLS ClientHello.
	_ = stream.SetReadDeadline(time.Now().Add(rsProbeTimeout)) //nolint:errcheck
	reply, err := readSocks5Reply(stream)
	if err != nil {
		closeBoth(conn, stream)
		return
	}
	if _, err := conn.Write(reply); err != nil {
		closeBoth(conn, stream)
		return
	}
	clearDeadlines(conn, stream)

	// 2. Mint a leaf for host. If we cannot (no minter / bad host), fall back to a
	//    verbatim splice: the browser's TLS goes straight to the origin unchanged.
	leaf, err := c.rs.minter.For(host)
	if err != nil {
		if c.appCl != nil {
			c.appCl.Log().Debugf("https range-split: no leaf for %s: %v; splicing", host, err)
		}
		c.splicePrefixed(conn, stream, nil)
		return
	}

	// 3. Terminate the browser TLS with our leaf, and open a verified TLS client to
	//    the real origin over the exit stream. Either handshake failing is fatal for
	//    this connection (we have already committed to interposing).
	btls := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{*leaf},
		MinVersion:   tls.VersionTLS12,
	})
	if err := btls.Handshake(); err != nil {
		closeBoth(btls, stream)
		return
	}
	otls := tls.Client(stream, c.originTLSConfig(host))
	if err := otls.Handshake(); err != nil {
		if c.appCl != nil {
			c.appCl.Log().Debugf("https range-split: origin %s handshake failed: %v", host, err)
		}
		closeBoth(btls, otls)
		return
	}

	// 4. Read the browser's first plaintext request. Non-HTTP or non-splittable
	//    traffic becomes a plaintext MITM splice, replaying what we read to the origin.
	reqHead, isHTTP := peekRequestHead(btls, rsHeadLimit)
	clearDeadlines(btls)
	if !isHTTP {
		c.splicePrefixed(btls, otls, reqHead)
		return
	}
	req, perr := http.ReadRequest(bufio.NewReader(bytes.NewReader(reqHead)))
	if perr != nil || !splittableRequest(req) {
		c.splicePrefixed(btls, otls, reqHead)
		return
	}

	// 5. Drive the split over the terminated browser conn (btls) and origin conn (otls).
	if !c.httpsRangeSplitDrive(btls, otls, req, reqHead, host) {
		// Nothing recoverable remains once we have started consuming the origin
		// response; drive() has already closed both ends on every exit path.
		return
	}
}

// httpsRangeSplitDrive performs the range probe on otls and, on a 206, reassembles
// the body onto btls. It returns true once it has fully served the response (split,
// verbatim relay, or a clean truncation) and closed both ends. reqHead is the raw
// request block; req is its parsed form.
func (c *Client) httpsRangeSplitDrive(btls, otls net.Conn, req *http.Request, reqHead []byte, host string) bool {
	// Probe: original request + Range: bytes=0-(chunk-1). A non-range origin ignores
	// it and returns its normal 200, kept byte-identical by the relay path below.
	if _, err := otls.Write(injectRange(reqHead, 0, c.rs.chunkSize-1)); err != nil {
		closeBoth(btls, otls)
		return true
	}
	br := bufio.NewReader(otls)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		closeBoth(btls, otls)
		return true
	}
	if statusCode(statusLine) != 206 {
		// Origin ignored Range (200) or returned redirect/error: relay verbatim so the
		// browser sees exactly what its unmodified GET would have produced.
		_, _ = btls.Write([]byte(statusLine)) //nolint:errcheck
		if n := br.Buffered(); n > 0 {
			b, _ := br.Peek(n)   //nolint:errcheck // peeking exactly Buffered() bytes never errors
			_, _ = btls.Write(b) //nolint:errcheck
		}
		_, _ = io.Copy(btls, otls) //nolint:errcheck
		closeBoth(btls, otls)
		return true
	}

	tp := textproto.NewReader(br)
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		closeBoth(btls, otls)
		return true
	}
	total, okTotal := parseContentRangeTotal(hdr.Get("Content-Range"))
	if !okTotal || total <= 0 {
		closeBoth(btls, otls)
		return true
	}
	validator := ifRangeValidator(hdr)

	if err := writeSynth200(btls, hdr, total); err != nil {
		closeBoth(btls, otls)
		return true
	}

	// chunk0 body straight from the probe response.
	chunk0Len := c.rs.chunkSize
	if total < chunk0Len {
		chunk0Len = total
	}
	if _, err := io.CopyN(btls, br, chunk0Len); err != nil {
		closeBoth(btls, otls)
		return true
	}
	otls.Close() //nolint:errcheck,gosec // origin stream0 done; remaining chunks use fresh TLS streams
	if total <= c.rs.chunkSize {
		btls.Close() //nolint:errcheck,gosec
		return true
	}

	if c.appCl != nil {
		c.appCl.Log().Debugf("https range-split: %s %d bytes → %d chunks × %d streams",
			host, total, numChunks(total, c.rs.chunkSize), c.rs.concurrency)
	}
	c.rsSplits.Add(1)
	c.rsChunks.Add(uint64(numChunks(total, c.rs.chunkSize))) //nolint:gosec // numChunks>0 here (total>chunkSize)
	c.rsBytes.Add(uint64(total))                             //nolint:gosec // total>0 checked above
	c.rsActive.Add(1)
	c.streamRemainingChunks(btls, total, func(start, end int64) ([]byte, error) {
		return c.fetchChunkTLSRetry(req, host, validator, start, end)
	})
	c.rsActive.Add(-1)
	btls.Close() //nolint:errcheck,gosec
	return true
}

// fetchChunkTLSRetry fetches one byte range over a fresh TLS-to-origin stream,
// redialing on churn — the HTTPS analogue of fetchChunkRetry.
func (c *Client) fetchChunkTLSRetry(req *http.Request, host, validator string, start, end int64) ([]byte, error) {
	var err error
	for i := 0; i < rsChunkRetries; i++ {
		var buf []byte
		buf, err = c.fetchChunkTLS(req, host, validator, start, end)
		if err == nil {
			return buf, nil
		}
	}
	return nil, err
}

// fetchChunkTLS opens a new exit stream, SOCKS5-CONNECTs to host:443, wraps it in a
// verified TLS client to the origin, issues a ranged GET carrying the original
// request's headers plus If-Range, and returns exactly the requested bytes.
func (c *Client) fetchChunkTLS(req *http.Request, host, validator string, start, end int64) ([]byte, error) {
	sess := c.pickSession()
	if sess == nil {
		return nil, errAllTunnelsDown
	}
	st, err := sess.Open()
	if err != nil {
		return nil, err
	}
	defer st.Close() //nolint:errcheck,gosec

	_ = st.SetDeadline(time.Now().Add(rsProbeTimeout)) //nolint:errcheck
	if err := c.exitConnect(st, host, 443); err != nil {
		return nil, err
	}
	tconn := tls.Client(st, c.originTLSConfig(host))
	if err := tconn.Handshake(); err != nil {
		return nil, err
	}
	if _, err := tconn.Write(buildRangedGet(req, host, validator, start, end)); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(tconn), req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("chunk %d-%d: status %d (validator changed?)", start, end, resp.StatusCode)
	}
	buf := make([]byte, end-start+1)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// closeBoth closes two conns, ignoring errors.
func closeBoth(a, b net.Conn) {
	a.Close() //nolint:errcheck,gosec
	b.Close() //nolint:errcheck,gosec
}
