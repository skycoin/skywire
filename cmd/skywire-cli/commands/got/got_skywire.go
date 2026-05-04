// Package cligot got_skywire.go — skynet:// and dmsg:// URL handling.
//
// `got` accepts http(s) URLs natively (chunked downloads, SOCKS5
// proxy support) and additionally accepts skynet://<pk>:<port>/path
// and dmsg://<pk>:<port>/path. The skywire schemes are routed
// through the local visor's RPC (SkynetHTTP / DmsgHTTP), so they
// require a running visor on the configured RPC address; the
// HTTP path runs without one. Falls back to no chunking on the
// skywire side — the visor RPC returns the full body in a single
// response, so range-based concurrency doesn't apply.
package cligot

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

// isSkywireURL reports whether the URL uses one of the visor-routed
// schemes (skynet:// or dmsg://). Trims whitespace defensively so
// shell-quoted args with stray spaces still match.
func isSkywireURL(rawURL string) bool {
	u := strings.TrimSpace(rawURL)
	return strings.HasPrefix(u, "skynet://") || strings.HasPrefix(u, "dmsg://")
}

// skywireTarget is the parsed form of a skynet:// or dmsg:// URL.
// Path always starts with "/" (defaulted when the URL has no path).
type skywireTarget struct {
	scheme string
	pk     cipher.PubKey
	port   uint16
	path   string
}

// parseSkywireURL splits a skynet:// or dmsg:// URL into its parts.
// Accepts skynet://<pk>:<port>/path, skynet://<pk>/path (port 80
// default), with an optional ".skynet"/".dmsg" host suffix that
// some users carry over from the address-bar form.
func parseSkywireURL(rawURL string) (skywireTarget, error) {
	rawURL = strings.TrimSpace(rawURL)
	var scheme string
	switch {
	case strings.HasPrefix(rawURL, "skynet://"):
		scheme = "skynet"
		rawURL = strings.TrimPrefix(rawURL, "skynet://")
	case strings.HasPrefix(rawURL, "dmsg://"):
		scheme = "dmsg"
		rawURL = strings.TrimPrefix(rawURL, "dmsg://")
	default:
		return skywireTarget{}, fmt.Errorf("expected skynet:// or dmsg:// URL")
	}

	path := "/"
	if idx := strings.Index(rawURL, "/"); idx >= 0 {
		path = rawURL[idx:]
		rawURL = rawURL[:idx]
	}

	host := rawURL
	port := uint16(80)
	if h, p, err := net.SplitHostPort(rawURL); err == nil {
		host = h
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return skywireTarget{}, fmt.Errorf("invalid port: %w", err)
		}
		port = uint16(n)
	}
	host = strings.TrimSuffix(strings.TrimSuffix(host, ".skynet"), ".dmsg")

	var pk cipher.PubKey
	if err := pk.Set(host); err != nil {
		return skywireTarget{}, fmt.Errorf("invalid public key %q: %w", host, err)
	}
	return skywireTarget{scheme: scheme, pk: pk, port: port, path: path}, nil
}

// skywireResponse is the unified response shape from either RPC.
type skywireResponse struct {
	statusCode int
	status     string
	header     map[string]string
	body       []byte
}

// requestSkywire issues the HTTP request through the visor's
// SkynetHTTP / DmsgHTTP RPC, depending on the target scheme.
func requestSkywire(cmd *cobra.Command, target skywireTarget, method string, hdr map[string]string, body []byte) (*skywireResponse, error) {
	if method == "" {
		method = http.MethodGet
	}
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		return nil, fmt.Errorf("RPC connection failed; is skywire running?: %w", err)
	}

	switch target.scheme {
	case "skynet":
		req := visor.SkynetHTTPRequest{
			PK:     target.pk,
			Port:   target.port,
			Path:   target.path,
			Method: method,
			Header: hdr,
			Body:   body,
		}
		resp, err := rpcClient.SkynetHTTP(req)
		if err != nil {
			return nil, fmt.Errorf("skynet request failed: %w", err)
		}
		return &skywireResponse{
			statusCode: resp.StatusCode,
			status:     resp.Status,
			header:     resp.Header,
			body:       resp.Body,
		}, nil
	case "dmsg":
		// DmsgHTTP wants the full URL string (it parses the host:port
		// internally), unlike SkynetHTTP which takes pk+port directly.
		urlStr := fmt.Sprintf("dmsg://%s:%d%s", target.pk.String(), target.port, target.path)
		req := visor.DmsgHTTPRequest{
			URL:    urlStr,
			Method: method,
			Header: hdr,
			Body:   body,
		}
		resp, err := rpcClient.DmsgHTTP(req)
		if err != nil {
			return nil, fmt.Errorf("dmsg request failed: %w", err)
		}
		return &skywireResponse{
			statusCode: resp.StatusCode,
			status:     resp.Status,
			header:     resp.Header,
			body:       resp.Body,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported scheme: %s", target.scheme)
	}
}

// downloadSkywire fetches the URL via the visor RPC and writes the
// response body to a file or stdout. Mirrors the HTTP `dl` UX: -o
// names the output file, otherwise derive from the URL path; "-"
// writes to stdout. No chunking — the RPC returns the full body
// in one response.
func downloadSkywire(cmd *cobra.Command, rawURL string) error {
	target, err := parseSkywireURL(rawURL)
	if err != nil {
		return err
	}

	hdrs, herr := buildHeaderMap(headers)
	if herr != nil {
		return herr
	}

	resp, err := requestSkywire(cmd, target, http.MethodGet, hdrs, nil)
	if err != nil {
		return err
	}
	if resp.statusCode >= 400 {
		fmt.Fprintf(os.Stderr, "%s\n", resp.status)
	}

	dest := output
	if dest == "" {
		dest = filepath.Base(target.path)
		if dest == "" || dest == "." || dest == "/" {
			dest = target.pk.String() + ".out"
		}
		if dir != "" {
			dest = filepath.Join(dir, dest)
		}
	}

	if dest == "-" {
		if _, err := os.Stdout.Write(resp.body); err != nil {
			return err
		}
		return nil
	}
	if err := os.WriteFile(dest, resp.body, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	fmt.Fprintf(os.Stderr, "%s\nSaved to %s (%d bytes)\n", resp.status, dest, len(resp.body))
	return nil
}

// requestSkywireCmd is the skywire equivalent of `got req`. Reads
// the body the same way (string, or @filename), supports custom
// headers, writes the response body to stdout or -o.
func requestSkywireCmd(cmd *cobra.Command, method, rawURL string) error {
	target, err := parseSkywireURL(rawURL)
	if err != nil {
		return err
	}

	hdrs, herr := buildHeaderMap(headers)
	if herr != nil {
		return herr
	}

	var bodyBytes []byte
	if data != "" {
		body, err := getBodyReader(data)
		if err != nil {
			return err
		}
		defer body.Close() //nolint:errcheck
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}
	}

	resp, err := requestSkywire(cmd, target, strings.ToUpper(method), hdrs, bodyBytes)
	if err != nil {
		return err
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "%s\n", resp.status)
		for k, v := range resp.header {
			fmt.Fprintf(os.Stderr, "%s: %s\n", k, v)
		}
		fmt.Fprintln(os.Stderr)
	}

	out := io.Writer(os.Stdout)
	if output != "" {
		f, err := os.Create(output) //nolint:gosec
		if err != nil {
			return err
		}
		defer f.Close() //nolint:errcheck
		out = f
	}
	if _, err := out.Write(resp.body); err != nil {
		return err
	}
	if resp.statusCode >= 400 {
		os.Exit(1)
	}
	return nil
}

// headSkywire emulates a HEAD request — the visor RPC has no HEAD
// method, so we issue a GET and only print headers. The response
// body is discarded. Good enough for "what does this endpoint say
// about itself" use cases.
func headSkywire(cmd *cobra.Command, rawURL string) error {
	target, err := parseSkywireURL(rawURL)
	if err != nil {
		return err
	}

	hdrs, herr := buildHeaderMap(headers)
	if herr != nil {
		return herr
	}

	resp, err := requestSkywire(cmd, target, http.MethodGet, hdrs, nil)
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", resp.status)
	for k, v := range resp.header {
		fmt.Printf("%s: %s\n", k, v)
	}
	return nil
}

// buildHeaderMap converts the "Key: Value" string slice (-H form)
// into the map[string]string the visor RPCs expect.
func buildHeaderMap(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, h := range raw {
		idx := strings.Index(h, ":")
		if idx < 0 {
			return nil, fmt.Errorf("invalid header %q (expected Key: Value)", h)
		}
		k := strings.TrimSpace(h[:idx])
		v := strings.TrimSpace(h[idx+1:])
		out[k] = v
	}
	return out, nil
}

// _ exists so context import survives if all other usages get
// pruned by future edits — context was used by the visor curl's
// signal-cancellation pattern; reintroduce it if we wire ctx into
// requestSkywire (currently sync via net/rpc).
var _ = context.Background
