// Package got is an HTTP client that downloads a file in concurrent byte
// ranges.
//
// A download starts with a one-byte range probe. If the server answers 206
// with a Content-Range, the file is split into chunks and fetched in
// parallel into a single sparse output file, each chunk writing at its own
// offset through an OffsetWriter. If the server ignores the range — no
// Content-Range, or a 200 with the whole body — the probe response *is* the
// download, and it streams straight to disk rather than being thrown away
// and refetched.
//
// Chunk progress is journaled beside the output file, so an interrupted
// download resumes from the chunks that finished rather than from zero.
//
// The package also carries the plain request helpers a downloader needs
// anyway — Got.Request, NewRequest, ParseHeaders, NormalizeURL — and a
// SOCKS5 constructor, NewWithProxy, that distinguishes socks5:// from
// socks5h:// the way curl does. That distinction matters for proxies which
// resolve names no DNS server knows.
//
// It began as a fork of github.com/melbahja/got and has since been
// substantially rewritten; see the README for what changed.
package got
