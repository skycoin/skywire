// Package cligot cmd/skywire-cli/commands/got/got.go
package cligot

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/got"
)

var (
	output      string
	dir         string
	concurrency uint
	chunkSize   uint64
	headers     []string
	proxyAddr   string
	userAgent   string
	resume      bool
	verbose     bool
	data        string
)

func init() {
	// Download flags
	dlCmd.Flags().StringVarP(&output, "output", "o", "", "output file path")
	dlCmd.Flags().StringVarP(&dir, "dir", "d", "", "output directory")
	dlCmd.Flags().UintVarP(&concurrency, "concurrency", "c", 0, "number of concurrent chunks (0 = auto)")
	dlCmd.Flags().Uint64Var(&chunkSize, "chunk-size", 0, "chunk size in bytes (0 = auto)")
	dlCmd.Flags().StringSliceVarP(&headers, "header", "H", nil, `HTTP header "Key: Value"`)
	dlCmd.Flags().StringVarP(&proxyAddr, "proxy", "x", "", "SOCKS5 proxy address (host:port)")
	dlCmd.Flags().StringVarP(&userAgent, "agent", "A", "", "user agent string")
	dlCmd.Flags().BoolVarP(&resume, "resume", "r", false, "resume interrupted download")

	// Request flags
	reqCmd.Flags().StringVarP(&output, "output", "o", "", "write response body to file")
	reqCmd.Flags().StringSliceVarP(&headers, "header", "H", nil, `HTTP header "Key: Value"`)
	reqCmd.Flags().StringVarP(&proxyAddr, "proxy", "x", "", "SOCKS5 proxy address (host:port)")
	reqCmd.Flags().StringVarP(&userAgent, "agent", "A", "", "user agent string")
	reqCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "print response headers")
	reqCmd.Flags().StringVarP(&data, "data", "D", "", `request body (or @filename to read from file)`)

	// Head flags
	headCmd.Flags().StringSliceVarP(&headers, "header", "H", nil, `HTTP header "Key: Value"`)
	headCmd.Flags().StringVarP(&proxyAddr, "proxy", "x", "", "SOCKS5 proxy address (host:port)")
	headCmd.Flags().StringVarP(&userAgent, "agent", "A", "", "user agent string")

	RootCmd.AddCommand(dlCmd, reqCmd, headCmd)
}

// RootCmd is the got command, defaults to download behavior.
var RootCmd = &cobra.Command{
	Use:   "got",
	Short: "HTTP client with concurrent downloads (also speaks skynet:// and dmsg://)",
	Long: `HTTP client with concurrent chunked downloads (RFC 7233),
SOCKS5 proxy support, and general-purpose HTTP requests.

URL schemes:
  http://, https://         standard HTTP, with chunked range downloads
  skynet://<pk>:<port>/path routed through the local visor (SkynetHTTP RPC)
  dmsg://<pk>:<port>/path   routed through the local visor (DmsgHTTP RPC)

Subcommands:
  got dl <URL>             chunked download (HTTP); single GET (skywire)
  got req <METHOD> <URL>   general request, any method
  got head <URL>           HEAD (HTTP) / GET-headers-only (skywire)

Default (no subcommand) is download.`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Default behavior: download
		dlCmd.Run(cmd, args)
	},
}

var dlCmd = &cobra.Command{
	Use:   "dl <URL> [URL...]",
	Short: "Download files (HTTP chunked, or single GET over skynet/dmsg)",
	Long: `Download via HTTP using concurrent chunked range requests (RFC 7233),
falling back to single-stream when the server doesn't support ranges.

skynet:// and dmsg:// URLs are routed through the local visor's
SkynetHTTP / DmsgHTTP RPC and complete in a single GET (no chunking).`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Skywire URLs go through the visor RPC. Process them first so
		// we don't spin up an HTTP client for an http-only flow.
		var httpURLs []string
		for _, rawURL := range args {
			if isSkywireURL(rawURL) {
				if err := downloadSkywire(cmd, rawURL); err != nil {
					fatal(err)
				}
				continue
			}
			httpURLs = append(httpURLs, rawURL)
		}
		if len(httpURLs) == 0 {
			return
		}

		g, err := newGot()
		if err != nil {
			fatal(err)
		}

		hdrs, err := got.ParseHeaders(headers)
		if err != nil {
			fatal(err)
		}

		g.ProgressFunc = progressFunc()

		for _, rawURL := range httpURLs {
			url, err := got.NormalizeURL(rawURL)
			if err != nil {
				fatal(err)
			}

			dl := &got.Download{
				URL:         url,
				Dir:         dir,
				Dest:        output,
				Header:      hdrs,
				Interval:    150,
				ChunkSize:   chunkSize,
				Concurrency: concurrency,
				Resume:      resume,
			}

			if err := g.Do(dl); err != nil {
				fatal(err)
			}

			fmt.Fprintf(os.Stderr, "\n%s\n", url)
		}
	},
}

var reqCmd = &cobra.Command{
	Use:   "req <METHOD> <URL>",
	Short: "Perform an HTTP request (also accepts skynet:// and dmsg://)",
	Long: `Perform a general HTTP request (GET, POST, PUT, DELETE, PATCH, etc).
URLs starting with skynet:// or dmsg:// are routed through the local
visor's SkynetHTTP / DmsgHTTP RPC.

Examples:
  got req GET https://example.com/api/data
  got req POST https://example.com/api -D '{"key":"value"}' -H "Content-Type: application/json"
  got req PUT https://example.com/api -D @payload.json
  got req GET skynet://02abc.../health
  got req POST dmsg://02abc.../api -D '{"k":"v"}'`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		method := strings.ToUpper(args[0])
		rawURL := args[1]

		if isSkywireURL(rawURL) {
			if err := requestSkywireCmd(cmd, method, rawURL); err != nil {
				fatal(err)
			}
			return
		}

		url, err := got.NormalizeURL(rawURL)
		if err != nil {
			fatal(err)
		}

		g, err := newGot()
		if err != nil {
			fatal(err)
		}

		hdrs, err := got.ParseHeaders(headers)
		if err != nil {
			fatal(err)
		}

		var body io.ReadCloser
		if data != "" {
			body, err = getBodyReader(data)
			if err != nil {
				fatal(err)
			}
			defer body.Close() //nolint:errcheck
		}

		var out io.Writer = os.Stdout
		if output != "" {
			f, err := os.Create(output) //nolint:gosec
			if err != nil {
				fatal(err)
			}
			defer f.Close() //nolint:errcheck
			out = f
		}

		resp, err := g.Request(method, url, hdrs, body, out)
		if err != nil {
			fatal(err)
		}

		if verbose {
			fmt.Fprintf(os.Stderr, "%s %s\n", resp.Proto, resp.Status)
			for k, vals := range resp.Header {
				for _, v := range vals {
					fmt.Fprintf(os.Stderr, "%s: %s\n", k, v)
				}
			}
			fmt.Fprintln(os.Stderr)
		}

		if resp.StatusCode >= 400 {
			os.Exit(1)
		}
	},
}

var headCmd = &cobra.Command{
	Use:   "head <URL>",
	Short: "Show response headers (HEAD on http; GET-headers on skynet/dmsg)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if isSkywireURL(args[0]) {
			// Visor RPC has no HEAD; we issue GET and only print
			// headers, discarding the body.
			if err := headSkywire(cmd, args[0]); err != nil {
				fatal(err)
			}
			return
		}
		url, err := got.NormalizeURL(args[0])
		if err != nil {
			fatal(err)
		}

		g, err := newGot()
		if err != nil {
			fatal(err)
		}

		hdrs, err := got.ParseHeaders(headers)
		if err != nil {
			fatal(err)
		}

		resp, err := g.Request("HEAD", url, hdrs, nil, nil)
		if err != nil {
			fatal(err)
		}

		fmt.Printf("%s %s\n", resp.Proto, resp.Status)
		for k, vals := range resp.Header {
			for _, v := range vals {
				fmt.Printf("%s: %s\n", k, v)
			}
		}
	},
}

func newGot() (*got.Got, error) {
	if userAgent != "" {
		got.UserAgent = userAgent
	}

	if proxyAddr != "" {
		return got.NewWithProxy(context.TODO(), proxyAddr)
	}

	return got.New(), nil
}

func getBodyReader(data string) (io.ReadCloser, error) {
	if strings.HasPrefix(data, "@") {
		filename := data[1:]
		f, err := os.Open(filename) //nolint:gosec
		if err != nil {
			return nil, fmt.Errorf("failed to open body file %q: %w", filename, err)
		}
		return f, nil
	}
	return io.NopCloser(strings.NewReader(data)), nil
}

func progressFunc() got.ProgressFunc {
	return func(d *got.Download) {
		total := d.TotalSize()
		current := d.Size()
		speed := d.Speed()

		if total > 0 {
			pct := float64(current) / float64(total) * 100
			fmt.Fprintf(os.Stderr, "\r %6.2f%% %s / %s @ %s/s",
				pct,
				humanBytes(current),
				humanBytes(total),
				humanBytes(speed),
			)
		} else {
			fmt.Fprintf(os.Stderr, "\r %s @ %s/s",
				humanBytes(current),
				humanBytes(speed),
			)
		}
	}
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "got: %v\n", err)
	os.Exit(1)
}
